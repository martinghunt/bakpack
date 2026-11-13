package bakpack

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestConvertINSDCMatchesBakta compares GenBank and EMBL with files written by
// Bakta 1.12.2's own writers; see testdata/bakta/README.md.
func TestConvertINSDCMatchesBakta(t *testing.T) {
	for _, tt := range []struct {
		json, fasta, want string
		compliant         bool
	}{
		{json: "synthetic.bakta.json", fasta: "synthetic.fa", want: "synthetic.bakta-1.12.2"},
		{json: "synthetic.bakta.json", fasta: "synthetic.fa", want: "synthetic.bakta-1.12.2.compliant", compliant: true},
		{json: "synthetic.v1.9.bakta.json", fasta: "synthetic.fa", want: "synthetic.v1.9.bakta-1.12.2"},
		{json: "synthetic.v1.9.bakta.json", fasta: "synthetic.fa", want: "synthetic.v1.9.bakta-1.12.2.compliant", compliant: true},
		{json: "synthetic.noncompliant.bakta.json", fasta: "synthetic.noncompliant.fa", want: "synthetic.noncompliant.bakta-1.12.2.compliant", compliant: true},
	} {
		annotation, err := os.ReadFile(filepath.Join("testdata", "bakta", tt.json))
		if err != nil {
			t.Fatal(err)
		}
		fasta, err := os.ReadFile(filepath.Join("testdata", "bakta", tt.fasta))
		if err != nil {
			t.Fatal(err)
		}
		genome := mustGenome(t, "synthetic", fasta)
		reduced, err := ReduceBaktaJSON(annotation, genome)
		if err != nil {
			t.Fatalf("%s: ReduceBaktaJSON() error = %v", tt.json, err)
		}
		for format, extension := range map[string]string{FormatGenBank: ".gbff", FormatEMBL: ".embl"} {
			want, err := os.ReadFile(filepath.Join("testdata", "bakta", tt.want+extension))
			if err != nil {
				t.Fatal(err)
			}
			for input, json := range map[string][]byte{"original": annotation, "reduced": reduced.ReducedJSON} {
				t.Run(tt.want+extension+" from "+input, func(t *testing.T) {
					got, err := ConvertBaktaJSON(json, genome, ConvertOptions{Format: format, Compliant: tt.compliant})
					if err != nil {
						t.Fatalf("ConvertBaktaJSON() error = %v", err)
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("%s differs from Bakta: %s", format, firstDiff(got, want))
					}
				})
			}
		}
	}
}
