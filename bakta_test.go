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

func TestReduceBaktaJSONErrorsOnUnsupportedFeatureType(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	annotation := bytes.Replace(toyBaktaJSON("sample1", "gene one"), []byte(`"type": "cds"`), []byte(`"type": "new-feature"`), 1)

	_, err := ReduceBaktaJSON(annotation, genome)
	if err == nil || !strings.Contains(err.Error(), `unsupported Bakta feature type "new-feature"`) {
		t.Fatalf("ReduceBaktaJSON() error = %v, want unsupported feature type error", err)
	}
}
