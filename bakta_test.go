package bakpack

import (
	"bytes"
	"strings"
	"testing"
)

func TestReduceAndRestoreBaktaJSONChecksCanonicalContent(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	original := toyBaktaJSON("sample1", "gene one")

	reduced, err := ReduceBaktaJSON(original, genome)
	if err != nil {
		t.Fatalf("ReduceBaktaJSON() error = %v", err)
	}
	if bytes.Contains(reduced.ReducedJSON, []byte(`"nt"`)) {
		t.Fatalf("reduced JSON still contains nt: %s", reduced.ReducedJSON)
	}
	if bytes.Contains(reduced.ReducedJSON, []byte(`"aa"`)) {
		t.Fatalf("reduced JSON still contains aa: %s", reduced.ReducedJSON)
	}
	if bytes.Contains(reduced.ReducedJSON, []byte(`"sequence"`)) {
		t.Fatalf("reduced JSON still contains sequence: %s", reduced.ReducedJSON)
	}
	for _, key := range []string{`"aa_hexdigest"`, `"start_type"`, `"length"`, `"no_sequences"`, `"n50"`} {
		if bytes.Contains(reduced.ReducedJSON, []byte(key)) {
			t.Fatalf("reduced JSON still contains derivable field %s: %s", key, reduced.ReducedJSON)
		}
	}
	if !bytes.Contains(reduced.ReducedJSON, []byte(`"_bakpack"`)) {
		t.Fatalf("reduced JSON missing bakpack metadata: %s", reduced.ReducedJSON)
	}
	if !bytes.Contains(reduced.ReducedJSON, []byte(reduced.Original.CanonicalSHA256)) {
		t.Fatalf("reduced JSON missing original canonical checksum: %s", reduced.ReducedJSON)
	}

	restored, err := RestoreBaktaJSON(reduced.ReducedJSON, genome)
	if err != nil {
		t.Fatalf("RestoreBaktaJSON() error = %v", err)
	}
	wantCanonical, err := JSONBytesCanonicalSHA256(original)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Original.CanonicalSHA256 != wantCanonical {
		t.Fatalf("restored canonical SHA = %s, want %s", restored.Original.CanonicalSHA256, wantCanonical)
	}

	tampered := bytes.Replace(reduced.ReducedJSON, []byte(reduced.Original.CanonicalSHA256), []byte(strings.Repeat("0", 64)), 1)
	if _, err := RestoreBaktaJSON(tampered, genome); err == nil || !strings.Contains(err.Error(), "original_json_canonical_sha256 mismatch") {
		t.Fatalf("RestoreBaktaJSON() with bad embedded checksum error = %v, want checksum mismatch", err)
	}
}

func TestReduceAndRestoreBaktaJSONv110Layout(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	original := toyBaktaJSONv110("sample1", "gene one")

	reduced, err := ReduceBaktaJSON(original, genome)
	if err != nil {
		t.Fatalf("ReduceBaktaJSON() error = %v", err)
	}
	for _, key := range []string{`"nt": "ATGAAATAA"`, `"aa"`, `"aa_hexdigest"`, `"start_type"`, `"length"`} {
		if bytes.Contains(reduced.ReducedJSON, []byte(key)) {
			t.Fatalf("reduced JSON still contains %s: %s", key, reduced.ReducedJSON)
		}
	}
	if !bytes.Contains(reduced.ReducedJSON, []byte(`"version": 2`)) {
		t.Fatalf("reduced JSON from v1.10 layout is not metadata version 2: %s", reduced.ReducedJSON)
	}

	restored, err := RestoreBaktaJSON(reduced.ReducedJSON, genome)
	if err != nil {
		t.Fatalf("RestoreBaktaJSON() error = %v", err)
	}
	if restored.Original.CanonicalSHA256 != reduced.Original.CanonicalSHA256 {
		t.Fatalf("restored canonical SHA = %s, want %s", restored.Original.CanonicalSHA256, reduced.Original.CanonicalSHA256)
	}
}

func TestReduceBaktaJSONLegacyLayoutIsMetadataVersion1(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	reduced, err := ReduceBaktaJSON(toyBaktaJSON("sample1", "gene one"), genome)
	if err != nil {
		t.Fatalf("ReduceBaktaJSON() error = %v", err)
	}
	if !bytes.Contains(reduced.ReducedJSON, []byte(`"version": 1`)) {
		t.Fatalf("reduced JSON from legacy layout is not metadata version 1: %s", reduced.ReducedJSON)
	}
}

func TestReduceAndRestoreBaktaJSONWithRenamedContigs(t *testing.T) {
	genome := mustGenome(t, "sample1", renamedContigFASTA())
	original := renamedContigBaktaJSON()

	reduced, err := ReduceBaktaJSON(original, genome)
	if err != nil {
		t.Fatalf("ReduceBaktaJSON() error = %v", err)
	}
	if len(reduced.UnmatchedSequences) != 0 {
		t.Fatalf("UnmatchedSequences = %v, want none", reduced.UnmatchedSequences)
	}
	for _, key := range []string{`"nt"`, `"aa"`, `"size"`} {
		if bytes.Contains(reduced.ReducedJSON, []byte(key)) {
			t.Fatalf("reduced JSON still contains %s: %s", key, reduced.ReducedJSON)
		}
	}
	// The edge CDS's literal translation does not match the forced-M
	// translation, so its digest must be kept for restore to pick the right one.
	if !bytes.Contains(reduced.ReducedJSON, []byte("2c16b194a0d9037002e9ef6580925697")) {
		t.Fatalf("reduced JSON dropped edge CDS aa_hexdigest: %s", reduced.ReducedJSON)
	}

	restored, err := RestoreBaktaJSON(reduced.ReducedJSON, genome)
	if err != nil {
		t.Fatalf("RestoreBaktaJSON() error = %v", err)
	}
	if restored.Original.CanonicalSHA256 != reduced.Original.CanonicalSHA256 {
		t.Fatalf("restored canonical SHA = %s, want %s", restored.Original.CanonicalSHA256, reduced.Original.CanonicalSHA256)
	}
	for _, absent := range []string{`"n50"`, `"n_ratio"`, `"no_sequences"`} {
		if bytes.Contains(restored.OriginalJSON, []byte(absent)) {
			t.Fatalf("restored JSON gained stats key %s absent from original: %s", absent, restored.OriginalJSON)
		}
	}
}

func TestReduceBaktaJSONReportsUnmatchedSequences(t *testing.T) {
	genome := mustGenome(t, "sample1", []byte(">other\nTTGAAATAAATGAAATAA\n"))
	reduced, err := ReduceBaktaJSON(renamedContigBaktaJSON(), genome)
	if err != nil {
		t.Fatalf("ReduceBaktaJSON() error = %v", err)
	}
	if len(reduced.UnmatchedSequences) != 1 || reduced.UnmatchedSequences[0] != "contig_1" {
		t.Fatalf("UnmatchedSequences = %v, want [contig_1]", reduced.UnmatchedSequences)
	}
	if !bytes.Contains(reduced.ReducedJSON, []byte(`"nt": "TTGAAATAAATGAAATAA"`)) {
		t.Fatalf("unmatched sequence nt was removed: %s", reduced.ReducedJSON)
	}
	if _, err := RestoreBaktaJSON(reduced.ReducedJSON, genome); err != nil {
		t.Fatalf("RestoreBaktaJSON() error = %v", err)
	}
}

func TestFeatureSpanRejectsOutOfRangeWraparoundStart(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1")) // contig1 is 9bp
	feature := map[string]any{"contig": "contig1", "start": 100, "stop": 2}

	if _, ok := featureSpan(feature, genome); ok {
		t.Fatal("featureSpan() with start beyond contig length = ok, want not ok")
	}
}

func TestReduceBaktaJSONErrorsOnUnsupportedFeatureType(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	annotation := bytes.Replace(toyBaktaJSON("sample1", "gene one"), []byte(`"type": "cds"`), []byte(`"type": "new-feature"`), 1)

	_, err := ReduceBaktaJSON(annotation, genome)
	if err == nil || !strings.Contains(err.Error(), `unsupported Bakta feature type "new-feature"`) {
		t.Fatalf("ReduceBaktaJSON() error = %v, want unsupported feature type error", err)
	}
}
