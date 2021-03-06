// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// Taken and extracted from
// https://github.com/juju/juju/blob/master/utils/ssh/authorisedkeys.go The
// functions in the original were too Juju specific and not something that
// could be used orthogonal in other packages. I've removed all third party
// dependencies and add the necessary functions into this package - arslan

package sshkeys

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

type ListMode bool

var (
	FullKeys     ListMode = true
	Fingerprints ListMode = false

	// We need a mutex because updates to the authorised keys file are done by
	// reading the contents, updating, and writing back out. So only one caller
	// at a time can use either Add, Delete, List.
	mutex sync.Mutex
)

const (
	authKeysFile = "authorized_keys"
)

type AuthorisedKey struct {
	Type    string
	Key     []byte
	Comment string
}

func authKeysDir(username string) (string, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return "", err
	}

	return filepath.Join(u.HomeDir, ".ssh"), nil
}

// ParseAuthorisedKey parses a non-comment line from an
// authorized_keys file and returns the constituent parts.
// Based on description in "man sshd".
func ParseAuthorisedKey(line string) (*AuthorisedKey, error) {
	key, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return nil, fmt.Errorf("invalid authorized_key %q", line)
	}
	return &AuthorisedKey{
		Type:    key.Type(),
		Key:     key.Marshal(),
		Comment: comment,
	}, nil
}

// KeyFingerprint returns the fingerprint and comment for the specified key
// in authorized_key format. Fingerprints are generated according to RFC4716.
// See ttp://www.ietf.org/rfc/rfc4716.txt, section 4.
func KeyFingerprint(key string) (fingerprint, comment string, err error) {
	ak, err := ParseAuthorisedKey(key)
	if err != nil {
		return "", "", fmt.Errorf("generating key fingerprint: %v", err)
	}
	hash := md5.New()
	hash.Write(ak.Key)
	sum := hash.Sum(nil)
	var buf bytes.Buffer
	for i := 0; i < hash.Size(); i++ {
		if i > 0 {
			buf.WriteByte(':')
		}
		buf.WriteString(fmt.Sprintf("%02x", sum[i]))
	}
	return buf.String(), ak.Comment, nil
}

// SplitAuthorisedKeys extracts a key slice from the specified key data,
// by splitting the key data into lines and ignoring comments and blank lines.
func SplitAuthorisedKeys(keyData string) []string {
	var keys []string
	for _, key := range strings.Split(string(keyData), "\n") {
		key = strings.Trim(key, " \r")
		if len(key) == 0 {
			continue
		}
		if key[0] == '#' {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

// AddKeys adds the specified ssh keys to the authorized_keys file for user.
// Returns an error if there is an issue with *any* of the supplied keys.
func AddKeys(user string, newKeys ...string) error {
	mutex.Lock()
	defer mutex.Unlock()
	existingKeys, err := readAuthorisedKeys(user)
	if err != nil {
		return err
	}
	for _, newKey := range newKeys {
		fingerprint, comment, err := KeyFingerprint(newKey)
		if err != nil {
			return err
		}
		if comment == "" {
			return fmt.Errorf("cannot add ssh key without comment")
		}
		for _, key := range existingKeys {
			existingFingerprint, existingComment, err := KeyFingerprint(key)
			if err != nil {
				// Only log a warning if the unrecognised key line is not a comment.
				if key[0] != '#' {
					log.Printf("invalid existing ssh key %q: %v", key, err)
				}
				continue
			}
			if existingFingerprint == fingerprint {
				return fmt.Errorf("cannot add duplicate ssh key: %v", fingerprint)
			}
			if existingComment == comment {
				return fmt.Errorf("cannot add ssh key with duplicate comment: %v", comment)
			}
		}
	}
	sshKeys := append(existingKeys, newKeys...)
	return writeAuthorisedKeys(user, sshKeys)
}

// DeleteKeys removes the specified ssh keys from the authorized ssh keys file for user.
// keyIds may be either key comments or fingerprints.
// Returns an error if there is an issue with *any* of the keys to delete.
func DeleteKeys(user string, keyIds ...string) error {
	mutex.Lock()
	defer mutex.Unlock()
	existingKeyData, err := readAuthorisedKeys(user)
	if err != nil {
		return err
	}
	// Build up a map of keys indexed by fingerprint, and fingerprints indexed by comment
	// so we can easily get the key represented by each keyId, which may be either a fingerprint
	// or comment.
	var keysToWrite []string
	var sshKeys = make(map[string]string)
	var keyComments = make(map[string]string)
	for _, key := range existingKeyData {
		fingerprint, comment, err := KeyFingerprint(key)
		if err != nil {
			log.Printf("keeping unrecognised existing ssh key %q: %v", key, err)
			keysToWrite = append(keysToWrite, key)
			continue
		}
		sshKeys[fingerprint] = key
		if comment != "" {
			keyComments[comment] = fingerprint
		}
	}

	for _, keyId := range keyIds {
		// assume keyId may be a fingerprint
		fingerprint := keyId
		_, ok := sshKeys[keyId]
		if !ok {
			// keyId is a comment
			fingerprint, ok = keyComments[keyId]
		}
		if !ok {
			return fmt.Errorf("cannot delete non existent key: %v", keyId)
		}
		delete(sshKeys, fingerprint)
	}

	for _, key := range sshKeys {
		keysToWrite = append(keysToWrite, key)
	}

	return writeAuthorisedKeys(user, keysToWrite)
}

// ReplaceKeys writes the specified ssh keys to the authorized_keys file for user,
// replacing any that are already there.
// Returns an error if there is an issue with *any* of the supplied keys.
func ReplaceKeys(user string, newKeys ...string) error {
	mutex.Lock()
	defer mutex.Unlock()

	existingKeyData, err := readAuthorisedKeys(user)
	if err != nil {
		return err
	}
	var existingNonKeyLines []string
	for _, line := range existingKeyData {
		_, _, err := KeyFingerprint(line)
		if err != nil {
			existingNonKeyLines = append(existingNonKeyLines, line)
		}
	}
	return writeAuthorisedKeys(user, append(existingNonKeyLines, newKeys...))
}

// ListKeys returns either the full keys or key comments from the authorized ssh keys file for user.
func ListKeys(user string, mode ListMode) ([]string, error) {
	mutex.Lock()
	defer mutex.Unlock()
	keyData, err := readAuthorisedKeys(user)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, key := range keyData {
		fingerprint, comment, err := KeyFingerprint(key)
		if err != nil {
			// Only log a warning if the unrecognised key line is not a comment.
			if key[0] != '#' {
				log.Printf("ignoring invalid ssh key %q: %v", key, err)
			}
			continue
		}
		if mode == FullKeys {
			keys = append(keys, key)
		} else {
			shortKey := fingerprint
			if comment != "" {
				shortKey += fmt.Sprintf(" (%s)", comment)
			}
			keys = append(keys, shortKey)
		}
	}
	return keys, nil
}

func readAuthorisedKeys(username string) ([]string, error) {
	keyDir, err := authKeysDir(username)
	if err != nil {
		return nil, err
	}
	sshKeyFile := filepath.Join(keyDir, authKeysFile)

	keyData, err := ioutil.ReadFile(sshKeyFile)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading ssh authorised keys file: %v", err)
	}
	var keys []string
	for _, key := range strings.Split(string(keyData), "\n") {
		if len(strings.Trim(key, " \r")) == 0 {
			continue
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func writeAuthorisedKeys(username string, keys []string) error {
	keyDir, err := authKeysDir(username)
	if err != nil {
		return err
	}
	err = os.MkdirAll(keyDir, os.FileMode(0755))
	if err != nil {
		return fmt.Errorf("cannot create ssh key directory: %v", err)
	}
	keyData := strings.Join(keys, "\n") + "\n"

	// Get perms to use on auth keys file
	sshKeyFile := filepath.Join(keyDir, authKeysFile)
	perms := os.FileMode(0644)
	info, err := os.Stat(sshKeyFile)
	if err == nil {
		perms = info.Mode().Perm()
	}

	log.Printf("writing authorised keys file %s", sshKeyFile)
	err = AtomicWriteFile(sshKeyFile, []byte(keyData), perms)
	if err != nil {
		return err
	}

	// TODO (wallyworld) - what to do on windows (if anything)
	// TODO(dimitern) - no need to use user.Current() if username
	// is "" - it will use the current user anyway.
	if runtime.GOOS != "windows" {
		// Ensure the resulting authorised keys file has its ownership
		// set to the specified username.
		var u *user.User
		if username == "" {
			u, err = user.Current()
		} else {
			u, err = user.Lookup(username)
		}
		if err != nil {
			return err
		}
		// chown requires ints but user.User has strings for windows.
		uid, err := strconv.Atoi(u.Uid)
		if err != nil {
			return err
		}
		gid, err := strconv.Atoi(u.Gid)
		if err != nil {
			return err
		}
		err = os.Chown(sshKeyFile, uid, gid)
		if err != nil {
			return err
		}
	}
	return nil
}

// Ensurecomment prepends the given comment to the given key. Any ssh key added
// to the authorised keys will have this prefix. This allows to know which
// keys have been added externally.
func EnsureComment(comment, key string) string {
	ak, err := ParseAuthorisedKey(key)
	// Just return an invalid key as is.
	if err != nil {
		log.Printf("invalid Koding ssh key %s: %v", key, err)
		return key
	}
	if ak.Comment == "" {
		return key + " " + comment + "sshkey"
	} else {
		// Add the Koding prefix to the comment if necessary.
		if !strings.HasPrefix(ak.Comment, comment) {
			commentIndex := strings.LastIndex(key, ak.Comment)
			return key[:commentIndex] + comment + ak.Comment
		}
	}
	return key
}

// AtomicWriteFile atomically writes the filename with the given
// contents and permissions, replacing any existing file at the same
// path.
func AtomicWriteFile(filename string, contents []byte, perms os.FileMode) (err error) {
	return AtomicWriteFileAndChange(filename, contents, func(f *os.File) error {
		// FileMod.Chmod() is not implemented on Windows, however, os.Chmod() is
		if err := os.Chmod(f.Name(), perms); err != nil {
			return fmt.Errorf("cannot set permissions: %v", err)
		}
		return nil
	})
}

// AtomicWriteFileAndChange atomically writes the filename with the
// given contents and calls the given function after the contents were
// written, but before the file is renamed.
func AtomicWriteFileAndChange(filename string, contents []byte, change func(*os.File) error) (err error) {
	dir, file := filepath.Split(filename)
	f, err := ioutil.TempFile(dir, file)
	if err != nil {
		return fmt.Errorf("cannot create temp file: %v", err)
	}
	defer f.Close()
	defer func() {
		if err != nil {
			// Don't leave the temp file lying around on error.
			// Close the file before removing. Trying to remove an open file on
			// Windows will fail.
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if _, err := f.Write(contents); err != nil {
		return fmt.Errorf("cannot write %q contents: %v", filename, err)
	}
	if err := change(f); err != nil {
		return err
	}
	f.Close()
	if err := os.Rename(f.Name(), filename); err != nil {
		return fmt.Errorf("cannot replace %q with %q: %v", f.Name(), filename, err)
	}
	return nil
}
