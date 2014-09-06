package popularpost

import (
	"testing"
	"time"

	"koding/db/mongodb/modelhelper"
	"socialapi/models"
	"socialapi/rest"
	"socialapi/workers/common/runner"
	"socialapi/workers/helper"

	"github.com/jinzhu/now"
	. "github.com/smartystreets/goconvey/convey"
)

func TestPopularPost(t *testing.T) {
	r := runner.New("popular post")
	if err := r.Init(); err != nil {
		panic(err)
	}
	defer r.Close()

	// initialize mongo
	modelhelper.Initialize(r.Conf.Mongo)

	// initialize redis
	helper.MustGetRedisConn()

	// initialize popular post controller
	controller := New(r.Log, helper.MustInitRedisConn(r.Conf))

	Convey("Popular post", t, func() {
		account, err := rest.CreateAccountInBothDbs()
		So(err, ShouldBeNil)

		c, err := rest.CreateChannel(account.Id)
		So(err, ShouldBeNil)

		cm, err := rest.CreatePost(c.Id, account.Id)
		So(err, ShouldBeNil)

		Convey("When an interaction arrives", func() {
			i, err := rest.AddInteraction("like", cm.Id, account.Id)
			So(err, ShouldBeNil)

			err = controller.InteractionSaved(i)
			So(err, ShouldBeNil)

			Convey("Interaction is saved in daily bucket", func() {
				keyname := &KeyName{
					GroupName: c.GroupName, ChannelName: c.Name,
					Time: cm.CreatedAt,
				}
				key := keyname.Today()

				// check if check exists
				exists := controller.redis.Exists(key)
				So(exists, ShouldEqual, true)

				// check for scores
				score, err := controller.redis.SortedSetScore(key, cm.Id)
				So(err, ShouldBeNil)
				So(score, ShouldEqual, 1)

				controller.redis.Del(key)
			})

			Convey("Interaction is saved in 7day bucket", func() {
				keyname := &KeyName{
					GroupName: c.GroupName, ChannelName: c.Name,
					Time: cm.CreatedAt,
				}
				key := keyname.Weekly()

				// check if check exists
				exists := controller.redis.Exists(key)
				So(exists, ShouldEqual, true)

				// check for scores
				score, err := controller.redis.SortedSetScore(key, cm.Id)
				So(err, ShouldBeNil)
				So(score, ShouldEqual, 1)

				controller.redis.Del(key)
			})
		})

		Convey("Posts with more interactions on same day have higher score", func() {
			acc2, err := rest.CreateAccountInBothDbs()
			So(err, ShouldBeNil)

			post2, err := rest.CreatePost(c.Id, account.Id)
			So(err, ShouldBeNil)

			// create 2 likes for post 1
			i, err := rest.AddInteraction("like", cm.Id, account.Id)
			So(err, ShouldBeNil)

			err = controller.InteractionSaved(i)
			So(err, ShouldBeNil)

			i, err = rest.AddInteraction("like", cm.Id, acc2.Id)
			So(err, ShouldBeNil)

			err = controller.InteractionSaved(i)
			So(err, ShouldBeNil)

			// create 1 likes for post 1
			i, err = rest.AddInteraction("like", post2.Id, account.Id)
			So(err, ShouldBeNil)

			err = controller.InteractionSaved(i)
			So(err, ShouldBeNil)

			// check if check exists
			keyname := &KeyName{
				GroupName: c.GroupName, ChannelName: c.Name,
				Time: cm.CreatedAt,
			}
			key := keyname.Weekly()

			exists := controller.redis.Exists(key)
			So(exists, ShouldEqual, true)

			// check for scores
			score, err := controller.redis.SortedSetScore(key, cm.Id)
			So(err, ShouldBeNil)
			So(score, ShouldEqual, 2)

			score, err = controller.redis.SortedSetScore(key, post2.Id)
			So(err, ShouldBeNil)
			So(score, ShouldEqual, 1)

			controller.redis.Del(key)
		})

		Convey("Posts with interactions today have higher score than yesterday", func() {
			todayPost, err := rest.CreatePost(c.Id, account.Id)
			So(err, ShouldBeNil)

			i, err := rest.AddInteraction("like", todayPost.Id, account.Id)
			So(err, ShouldBeNil)

			err = controller.InteractionSaved(i)
			So(err, ShouldBeNil)

			// create post with interaction yesterday
			yesterdayPost := models.NewChannelMessage()
			yesterdayPost.AccountId = account.Id
			yesterdayPost.InitialChannelId = c.Id
			yesterdayPost.Body = "yesterday my troubles were so far away"

			err = yesterdayPost.Create()
			So(err, ShouldBeNil)

			err = yesterdayPost.UpdateCreatedAt(now.BeginningOfDay().Add(-24 * time.Hour))
			So(err, ShouldBeNil)

			i, err = rest.AddInteraction("like", yesterdayPost.Id, account.Id)
			So(err, ShouldBeNil)

			err = controller.InteractionSaved(i)
			So(err, ShouldBeNil)

			// create post with interaction yesterday
			twoDaysAgo := models.NewChannelMessage()
			twoDaysAgo.AccountId = account.Id
			twoDaysAgo.InitialChannelId = c.Id
			twoDaysAgo.Body = "yesterday my troubles were so far away"

			err = twoDaysAgo.Create()
			So(err, ShouldBeNil)

			err = twoDaysAgo.UpdateCreatedAt(now.BeginningOfDay().Add(-48 * time.Hour))
			So(err, ShouldBeNil)

			i, err = rest.AddInteraction("like", twoDaysAgo.Id, account.Id)
			So(err, ShouldBeNil)

			err = controller.InteractionSaved(i)
			So(err, ShouldBeNil)

			// check if check exists
			keyname := &KeyName{
				GroupName: c.GroupName, ChannelName: c.Name,
				Time: time.Now().UTC(),
			}
			key := keyname.Weekly()

			exists := controller.redis.Exists(key)
			So(exists, ShouldEqual, true)

			// check for scores
			score, err := controller.redis.SortedSetScore(key, todayPost.Id)
			So(err, ShouldBeNil)
			So(score, ShouldEqual, 1)

			score, err = controller.redis.SortedSetScore(key, yesterdayPost.Id)
			So(err, ShouldBeNil)
			So(score, ShouldEqual, 0.5)

			score, err = controller.redis.SortedSetScore(key, twoDaysAgo.Id)
			So(err, ShouldBeNil)
			So(score, ShouldEqual, 0.3)
		})
	})
}
