package bakpack

import (
	"strings"
	"testing"
	"time"
)

func TestFormatExtension(t *testing.T) {
	for format, want := range map[string]string{"": ".gff3", "gff3": ".gff3", "GenBank": ".gbff", "embl": ".embl"} {
		got, err := FormatExtension(format)
		if err != nil || got != want {
			t.Errorf("FormatExtension(%q) = %q, %v; want %q", format, got, err, want)
		}
	}
	if _, err := FormatExtension("fasta"); err == nil || !strings.Contains(err.Error(), "unknown output format") {
		t.Errorf("FormatExtension(fasta) error = %v, want unknown output format", err)
	}
}

func TestAnnotationDate(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 11, 13, 14, 15, 16, 999, time.UTC) }
	withRun := map[string]any{"run": map[string]any{"start": "2026-01-01 00:00:00", "end": "2026-01-02 03:04:05"}}
	noRun := map[string]any{}
	for _, tt := range []struct {
		name     string
		data     map[string]any
		fallback string
		want     time.Time
		wantErr  string
	}{
		{name: "run end", data: withRun, want: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{name: "run end wins over fallback", data: withRun, fallback: "2020-05-06", want: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{name: "no run and no fallback", data: noRun, wantErr: "--annotation-date"},
		{name: "date", data: noRun, fallback: "2020-05-06", want: time.Date(2020, 5, 6, 0, 0, 0, 0, time.UTC)},
		{name: "date and time", data: noRun, fallback: "2020-05-06 07:08:09", want: time.Date(2020, 5, 6, 7, 8, 9, 0, time.UTC)},
		{name: "today", data: noRun, fallback: "Today", want: time.Date(2026, 11, 13, 14, 15, 16, 0, time.UTC)},
		{name: "invalid fallback", data: noRun, fallback: "06/05/2020", wantErr: "not YYYY-MM-DD"},
		{name: "invalid run end", data: map[string]any{"run": map[string]any{"end": "yesterday"}}, wantErr: "run end"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := annotationDate(tt.data, tt.fallback, now)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("annotationDate() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !got.Equal(tt.want) {
				t.Fatalf("annotationDate() = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestConvertGenBankNeedsDate(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	for _, format := range []string{FormatGenBank, FormatEMBL} {
		_, err := ConvertBaktaJSON(toyBaktaJSONv110("sample1", "gene one"), genome, ConvertOptions{Format: format})
		if err == nil || !strings.Contains(err.Error(), `no "run" section`) {
			t.Errorf("%s without run or annotation date: error = %v, want missing run", format, err)
		}
	}
}
