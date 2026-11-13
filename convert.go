package bakpack

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/martinghunt/faqt/insdc"
)

// Output formats for ConvertBaktaJSON.
const (
	FormatGFF3    = "gff3"
	FormatGenBank = "genbank"
	FormatEMBL    = "embl"
)

// formatExtensions are the file name extensions bakpack writes for each format,
// as Bakta names its own output files.
var formatExtensions = map[string]string{
	FormatGFF3:    ".gff3",
	FormatGenBank: ".gbff",
	FormatEMBL:    ".embl",
}

// ConvertOptions configures converting Bakta JSON to GFF3, GenBank or EMBL.
type ConvertOptions struct {
	// Format is FormatGFF3 (the default when empty), FormatGenBank or
	// FormatEMBL.
	Format string
	// Compliant writes the output as Bakta's --compliant (INSDC) mode does.
	// It also applies the changes Bakta's --compliant mode makes while
	// annotating, so the output is compliant even when Bakta was run without
	// --compliant.
	Compliant bool
	// AnnotationOnly omits GFF3's terminal ##FASTA section. GenBank and EMBL
	// always include the sequence.
	AnnotationOnly bool
	// AnnotationDate is the date GenBank and EMBL output use when the JSON
	// has no "run" section, which Bakta always writes. It is YYYY-MM-DD,
	// "YYYY-MM-DD HH:MM:SS", or "today" for the current date and time. It is
	// ignored when the JSON has a run section. Without either, GenBank and
	// EMBL conversion fails.
	AnnotationDate string
	// BakpackVersion overrides the version in GFF3's generated-by header.
	// When empty, the build-time bakpack version is used.
	BakpackVersion string
	// Warn, if set, receives problems found in compliant mode that bakpack
	// cannot fix, and contigs it leaves out.
	Warn func(message string)
}

// FormatExtension returns the file name extension for a ConvertOptions
// format: ".gff3", ".gbff" or ".embl".
func FormatExtension(format string) (string, error) {
	format, err := normalizeFormat(format)
	if err != nil {
		return "", err
	}
	return formatExtensions[format], nil
}

// normalizeFormat returns the canonical name of format, which may be empty for
// GFF3 and is matched without regard to case.
func normalizeFormat(format string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return FormatGFF3, nil
	}
	if _, ok := formatExtensions[format]; !ok {
		return "", fmt.Errorf("unknown output format %q (want gff3, genbank or embl)", format)
	}
	return format, nil
}

// ConvertBaktaJSON writes a Bakta JSON annotation as GFF3, GenBank or EMBL, as
// current Bakta writes it for that JSON. The input may be original JSON or
// bakpack reduced JSON; reduced input is restored first.
func ConvertBaktaJSON(annotation []byte, genome Genome, opts ConvertOptions) ([]byte, error) {
	format, err := normalizeFormat(opts.Format)
	if err != nil {
		return nil, err
	}
	data, err := decodeBaktaForConversion(annotation, genome)
	if err != nil {
		return nil, err
	}
	if format == FormatGFF3 {
		return writeBaktaGFF3(data, BaktaGFF3Options{
			AnnotationOnly: opts.AnnotationOnly,
			BakpackVersion: opts.BakpackVersion,
			Compliant:      opts.Compliant,
			Warn:           opts.Warn,
		})
	}
	date, err := annotationDate(data, opts.AnnotationDate, time.Now)
	if err != nil {
		return nil, err
	}
	records, err := buildBaktaINSDCRecords(data, opts.Compliant, date, opts.Warn)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if format == FormatGenBank {
		err = insdc.WriteGenBank(&buf, records...)
	} else {
		err = insdc.WriteEMBL(&buf, records...)
	}
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeBaktaForConversion decodes Bakta JSON, restoring reduced JSON, and
// checks it can be converted.
func decodeBaktaForConversion(annotation []byte, genome Genome) (map[string]any, error) {
	root, err := DecodeJSON(annotation)
	if err != nil {
		return nil, err
	}
	data, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Bakta JSON root is not an object")
	}
	if _, ok := data[reducedMetadataKey]; ok {
		restored, err := RestoreBaktaJSON(annotation, genome)
		if err != nil {
			return nil, err
		}
		root, err = DecodeJSON(restored.OriginalJSON)
		if err != nil {
			return nil, err
		}
		data, ok = root.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Bakta JSON root is not an object")
		}
	}
	if err := validateBaktaFeatureTypes(data); err != nil {
		return nil, err
	}
	if err := checkConversionInput(data); err != nil {
		return nil, err
	}
	return data, nil
}

const baktaRunTimeLayout = "2006-01-02 15:04:05"

// annotationDate returns the end of the Bakta run recorded in data, which
// GenBank and EMBL output are dated with, or else fallback ("today", a date,
// or a date and time).
func annotationDate(data map[string]any, fallback string, now func() time.Time) (time.Time, error) {
	if run, ok := data["run"].(map[string]any); ok {
		end, _ := run["end"].(string)
		date, err := time.Parse(baktaRunTimeLayout, end)
		if err != nil {
			return time.Time{}, fmt.Errorf("JSON run end %q is not a date and time like 2006-01-02 15:04:05", end)
		}
		return date, nil
	}
	fallback = strings.TrimSpace(fallback)
	switch {
	case fallback == "":
		return time.Time{}, fmt.Errorf(`JSON has no "run" section, which Bakta writes and GenBank/EMBL dates come from; give an annotation date (--annotation-date)`)
	case strings.EqualFold(fallback, "today"):
		return now().Truncate(time.Second), nil
	}
	for _, layout := range []string{baktaRunTimeLayout, "2006-01-02"} {
		if date, err := time.Parse(layout, fallback); err == nil {
			return date, nil
		}
	}
	return time.Time{}, fmt.Errorf("annotation date %q is not YYYY-MM-DD, YYYY-MM-DD HH:MM:SS or today", fallback)
}
