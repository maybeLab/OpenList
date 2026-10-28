package smb

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/server/common"
)

type smbProxyFixture struct {
	SMB
	file  *smbTrackedFile
	calls int
}

func (d *smbProxyFixture) Get(context.Context, string) (model.Obj, error) {
	return &model.Object{Name: "video.mp4", Path: "/video.mp4", Size: d.file.Size()}, nil
}

func (d *smbProxyFixture) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	d.calls++
	return &model.Link{
		RangeReader:      newSMBRangeReader(d.file.Size(), d.file),
		SyncClosers:      utils.NewSyncClosers(d.file),
		RequireReference: true,
	}, nil
}

func TestSMBProxyUsesLiveRangeContextWithCachedLink(t *testing.T) {
	previous := conf.Conf
	conf.Conf = conf.DefaultConfig(t.TempDir())
	t.Cleanup(func() { conf.Conf = previous })
	const content = "0123456789abcdefghijklmnopqrstuvwxyz"
	d := &smbProxyFixture{file: &smbTrackedFile{Reader: bytes.NewReader([]byte(content))}}
	d.Storage = model.Storage{MountPath: "/" + t.Name()}
	acquire := func(ctx context.Context) (*model.Link, model.Obj) {
		t.Helper()
		link, obj, err := op.Link(ctx, d, "/video.mp4", model.LinkArgs{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = link.Close() })
		return link, obj
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, _ := acquire(ctx)
	second, obj := acquire(context.Background())
	cancel()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// Repeated seeks must reuse a live link after the initiating request is canceled.
	for start := range 30 {
		link, _ := acquire(context.Background())
		request := httptest.NewRequest(http.MethodGet, "/proxy/video.mp4", nil)
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, start+3))
		response := httptest.NewRecorder()
		if err := common.Proxy(response, request, link, obj); err != nil {
			t.Fatalf("seek %d: %v", start, err)
		}
		if response.Code != http.StatusPartialContent || response.Body.String() != content[start:start+4] {
			t.Fatalf("seek %d: status=%d, body=%q", start, response.Code, response.Body.String())
		}
		if response.Header().Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", start, start+3, len(content)) {
			t.Fatalf("seek %d: Content-Range=%q", start, response.Header().Get("Content-Range"))
		}
		if err := link.Close(); err != nil {
			t.Fatal(err)
		}
		if d.file.closes.Load() != 0 {
			t.Fatal("proxy closed the shared file while another reference was active")
		}
	}
	if d.calls != 1 {
		t.Fatalf("created %d links, want one cached link", d.calls)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if d.file.closes.Load() != 1 {
		t.Fatalf("final file closes = %d, want 1", d.file.closes.Load())
	}
}
