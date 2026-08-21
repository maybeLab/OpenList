package thumbnail

import (
	"net/url"
	"os"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "openlist-thumbnail-tests-")
	if err != nil {
		panic(err)
	}
	conf.Conf = conf.DefaultConfig(root)
	conf.URL = &url.URL{Path: "/"}
	conf.SlicesMap[conf.ImageTypes] = []string{"jpg", "jpeg", "png", "webp"}
	conf.SlicesMap[conf.VideoTypes] = []string{"mp4", "mkv", "webm"}
	database, err := gorm.Open(sqlite.Open("file:thumbnail-tests?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	db.Init(database)
	code := m.Run()
	db.Close()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
