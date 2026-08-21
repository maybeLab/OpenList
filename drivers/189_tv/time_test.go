package _189_tv

import (
	"encoding/json"
	"encoding/xml"
	"testing"
	"time"
)

func TestTimeUnmarshalSupportedFormats(t *testing.T) {
	want := time.Date(2026, time.August, 11, 23, 17, 26, 0, time.FixedZone("UTC+8", 8*60*60))
	for _, value := range []string{
		"2026-08-11 23:17:26",
		"Aug 11, 2026 23:17:26 PM",
		"Aug 11, 2026, 11:17:26 PM",
		"Aug 11, 2026, 11:17:26\u202fPM",
		"Aug 11, 2026, 11:17:26\u00a0PM",
	} {
		t.Run(value, func(t *testing.T) {
			var got Time
			if err := got.Unmarshal([]byte(value)); err != nil {
				t.Fatalf("Unmarshal(%q) error: %v", value, err)
			}
			gotTime := time.Time(got)
			if !gotTime.Equal(want) {
				t.Fatalf("Unmarshal(%q) = %s, want %s", value, gotTime, want)
			}
			_, offset := gotTime.Zone()
			if offset != 8*60*60 {
				t.Fatalf("Unmarshal(%q) timezone offset = %d, want %d", value, offset, 8*60*60)
			}
		})
	}
}

func TestTimeUnmarshalJSONAndXML(t *testing.T) {
	var jsonValue struct {
		CreateDate Time `json:"createDate"`
	}
	if err := json.Unmarshal([]byte(`{"createDate":"Aug 11, 2026, 11:17:26\u202fPM"}`), &jsonValue); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	if time.Time(jsonValue.CreateDate).Hour() != 23 {
		t.Fatalf("JSON createDate = %s, want hour 23", time.Time(jsonValue.CreateDate))
	}

	var xmlValue struct {
		CreateDate Time `xml:"createDate"`
	}
	if err := xml.Unmarshal([]byte(`<file><createDate>Aug 11, 2026, 11:17:26&#160;PM</createDate></file>`), &xmlValue); err != nil {
		t.Fatalf("xml.Unmarshal() error: %v", err)
	}
	if time.Time(xmlValue.CreateDate).Hour() != 23 {
		t.Fatalf("XML createDate = %s, want hour 23", time.Time(xmlValue.CreateDate))
	}
}

func TestTimeUnmarshalRejectsInvalidInput(t *testing.T) {
	var got Time
	if err := got.Unmarshal([]byte("not-a-time")); err == nil {
		t.Fatal("Unmarshal(invalid) error = nil")
	}
}
