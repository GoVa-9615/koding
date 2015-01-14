package models

import (
	"errors"

	"github.com/koding/bongo"
	"github.com/nu7hatch/gouuid"
)

func NewAccount() *Account {
	return &Account{}
}

func (a Account) GetId() int64 {
	return a.Id
}

func (a Account) BongoName() string {
	return "api.account"
}

func (a *Account) BeforeCreate() error {
	return a.createToken()
}

func (a *Account) BeforeUpdate() error {
	return a.createToken()
}

func (a *Account) createToken() error {
	if a.Token == "" {
		token, err := uuid.NewV4()
		if err != nil {
			return err
		}
		a.Token = token.String()
	}

	return nil
}

func (a *Account) AfterUpdate() {
	SetAccountToCache(a)
}

func (a *Account) AfterCreate() {
	SetAccountToCache(a)
	bongo.B.AfterCreate(a)
}

func (a *Account) One(q *bongo.Query) error {
	return bongo.B.One(a, a, q)
}

func (a *Account) ById(id int64) error {
	return bongo.B.ById(a, id)
}

func (a *Account) Update() error {
	return bongo.B.Update(a)
}

func (a *Account) Create() error {
	if a.OldId == "" {
		return errors.New("old id is not set")
	}

	return bongo.B.Create(a)
}

func (a *Account) Some(data interface{}, q *bongo.Query) error {
	return bongo.B.Some(a, data, q)
}
