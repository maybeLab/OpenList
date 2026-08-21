package _189pc

import (
	"encoding/json"
	"encoding/xml"
	"testing"
	"time"
)

func TestTimeUnmarshalSupportedFormats(t *testing.T) {
	want := time.Date(2026, time.August, 11, 23, 17, 26, 0, time.FixedZone("UTC+8", 8*60*60))
	tests := []struct {
		name  string
		value string
	}{
		{name: "numeric legacy", value: "2026-08-11 23:17:26"},
		{name: "english legacy", value: "Aug 11, 2026 23:17:26 PM"},
		{name: "new ordinary space", value: "Aug 11, 2026, 11:17:26 PM"},
		{name: "new narrow no-break space", value: "Aug 11, 2026, 11:17:26\u202fPM"},
		{name: "new no-break space", value: "Aug 11, 2026, 11:17:26\u00a0PM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Time
			if err := got.Unmarshal([]byte(tt.value)); err != nil {
				t.Fatalf("Unmarshal(%q) error: %v", tt.value, err)
			}
			gotTime := time.Time(got)
			if !gotTime.Equal(want) {
				t.Fatalf("Unmarshal(%q) = %s, want %s", tt.value, gotTime, want)
			}
			_, offset := gotTime.Zone()
			if offset != 8*60*60 {
				t.Fatalf("Unmarshal(%q) timezone offset = %d, want %d", tt.value, offset, 8*60*60)
			}
		})
	}
}

func TestTimeUnmarshalRejectsInvalidInput(t *testing.T) {
	var got Time
	if err := got.Unmarshal([]byte("not-a-time")); err == nil {
		t.Fatal("Unmarshal(invalid) error = nil")
	}
}

func TestCommitMultiUploadFileRespUnmarshalNewCreateDate(t *testing.T) {
	const body = `{"file":{"userFileId":"file-id","fileName":"thumb.webp","fileSize":123,"fileMd5":"abc","createDate":"Aug 11, 2026, 11:17:26\u202fPM"}}`
	var got CommitMultiUploadFileResp
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	gotTime := time.Time(got.File.CreateDate)
	if gotTime.Hour() != 23 || gotTime.Minute() != 17 || gotTime.Second() != 26 {
		t.Fatalf("createDate = %s, want 23:17:26", gotTime)
	}
	_, offset := gotTime.Zone()
	if offset != 8*60*60 {
		t.Fatalf("createDate timezone offset = %d, want %d", offset, 8*60*60)
	}
}

func TestTimeUnmarshalXMLNewCreateDate(t *testing.T) {
	var got struct {
		CreateDate Time `xml:"createDate"`
	}
	const body = `<file><createDate>Aug 11, 2026, 11:17:26&#8239;PM</createDate></file>`
	if err := xml.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("xml.Unmarshal() error: %v", err)
	}
	gotTime := time.Time(got.CreateDate)
	if gotTime.Hour() != 23 {
		t.Fatalf("createDate = %s, want hour 23", gotTime)
	}
}
