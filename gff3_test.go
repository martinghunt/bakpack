package bakpack

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/martinghunt/bakpack/internal/buildinfo"
)

func TestBaktaGFF3RejectsInvalidSequenceLength(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	original := []byte(`{
  "genome": {"translation_table": 11},
  "sequences": [
    {"id": "contig1", "length": -3, "sequence": "ATGAAATAA"}
  ],
  "features": []
}`)
	if _, err := BaktaGFF3(original, genome); err == nil {
		t.Fatal("BaktaGFF3() with negative sequence length = nil error, want error")
	}
}

func TestBaktaGFF3RejectsFeatureMissingCoordinates(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	original := []byte(`{
  "genome": {"translation_table": 11},
  "sequences": [
    {"id": "contig1", "length": 9, "sequence": "ATGAAATAA"}
  ],
  "features": [
    {"type": "cds", "contig": "contig1", "strand": "+"}
  ]
}`)
	if _, err := BaktaGFF3(original, genome); err == nil {
		t.Fatal("BaktaGFF3() with feature missing start/stop = nil error, want error")
	}
}

func TestBaktaGFF3RejectsCRISPRFeatureMissingCoordinates(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	original := []byte(`{
  "genome": {"translation_table": 11},
  "sequences": [
    {"id": "contig1", "length": 9, "sequence": "ATGAAATAA"}
  ],
  "features": [
    {"type": "crispr", "contig": "contig1", "id": "crispr_1"}
  ]
}`)
	if _, err := BaktaGFF3(original, genome); err == nil {
		t.Fatal("BaktaGFF3() with CRISPR feature missing start/stop = nil error, want error")
	}
}

func TestBaktaGFF3RejectsInvalidFeatureCoordinates(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	original := []byte(`{
  "genome": {"translation_table": 11},
  "sequences": [
    {"id": "contig1", "length": 9, "sequence": "ATGAAATAA"}
  ],
  "features": [
    {"type": "cds", "contig": "contig1", "start": 0, "stop": 9, "strand": "+"}
  ]
}`)
	if _, err := BaktaGFF3(original, genome); err == nil {
		t.Fatal("BaktaGFF3() with zero feature start = nil error, want error")
	}
}

func TestBaktaGFF3AllowsWraparoundFeatureCoordinates(t *testing.T) {
	// start > stop is the deliberate convention for a feature that wraps
	// around the origin of a circular contig; it must not be rejected.
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	original := []byte(`{
  "genome": {"translation_table": 11},
  "sequences": [
    {"id": "contig1", "length": 9, "sequence": "ATGAAATAA"}
  ],
  "features": [
    {"type": "gap", "contig": "contig1", "start": 8, "stop": 2, "length": 4}
  ]
}`)
	if _, err := BaktaGFF3(original, genome); err != nil {
		t.Fatalf("BaktaGFF3() with wraparound coordinates error = %v, want nil", err)
	}
}

func TestBaktaGFF3WritesOriVAsOriC(t *testing.T) {
	// Bakta labels plasmid replication origins oriV in JSON but writes the
	// oriC SO term in GFF3.
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	original := []byte(`{
  "genome": {"translation_table": 11},
  "sequences": [
    {"id": "contig1", "length": 9, "sequence": "ATGAAATAA"}
  ],
  "features": [
    {"type": "oriV", "contig": "contig1", "start": 2, "stop": 7, "strand": "?", "id": "ori_1", "product": "origin of replication"}
  ]
}`)
	gff, err := BaktaGFF3(original, genome)
	if err != nil {
		t.Fatalf("BaktaGFF3() error = %v", err)
	}
	want := "contig1\tBLAST+\toriC\t2\t7\t.\t?\t.\tID=ori_1;Name=origin of replication;product=origin of replication;inference=similar to DNA sequence\n"
	if !strings.Contains(string(gff), want) {
		t.Fatalf("BaktaGFF3() missing oriV line %q in:\n%s", want, gff)
	}
}

// TestBaktaGFF3MatchesBakta112 compares bakpack's GFF3 with GFF3 written by
// Bakta 1.12.2's own writer; see testdata/bakta/README.md.
func TestBaktaGFF3MatchesBakta112(t *testing.T) {
	annotation, err := os.ReadFile(filepath.Join("testdata", "bakta", "synthetic.bakta.json"))
	if err != nil {
		t.Fatal(err)
	}
	fasta, err := os.ReadFile(filepath.Join("testdata", "bakta", "synthetic.fa"))
	if err != nil {
		t.Fatal(err)
	}
	genome := mustGenome(t, "synthetic", fasta)
	reduced, err := ReduceBaktaJSON(annotation, genome)
	if err != nil {
		t.Fatalf("ReduceBaktaJSON() error = %v", err)
	}
	for _, tt := range []struct {
		name      string
		compliant bool
		want      string
	}{
		{name: "default", want: "synthetic.bakta-1.12.2.gff3"},
		{name: "compliant", compliant: true, want: "synthetic.bakta-1.12.2.compliant.gff3"},
	} {
		want, err := os.ReadFile(filepath.Join("testdata", "bakta", tt.want))
		if err != nil {
			t.Fatal(err)
		}
		for input, json := range map[string][]byte{"original": annotation, "reduced": reduced.ReducedJSON} {
			t.Run(tt.name+" from "+input+" JSON", func(t *testing.T) {
				got, err := BaktaGFF3WithOptions(json, genome, BaktaGFF3Options{Compliant: tt.compliant})
				if err != nil {
					t.Fatalf("BaktaGFF3WithOptions() error = %v", err)
				}
				got = stripBakpackGFF3Header(got)
				if !bytes.Equal(got, want) {
					t.Fatalf("GFF3 differs from Bakta 1.12.2 output: %s", firstDiff(got, want))
				}
			})
		}
	}
}

// TestCompliantGFF3FromNonCompliantJSON checks compliant output for JSON from
// a Bakta run without --compliant against Bakta's own code: its annotation
// changes followed by its compliant GFF3 writer (see testdata/bakta/README.md).
func TestCompliantGFF3FromNonCompliantJSON(t *testing.T) {
	annotation, err := os.ReadFile(filepath.Join("testdata", "bakta", "synthetic.noncompliant.bakta.json"))
	if err != nil {
		t.Fatal(err)
	}
	fasta, err := os.ReadFile(filepath.Join("testdata", "bakta", "synthetic.noncompliant.fa"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "bakta", "synthetic.noncompliant.bakta-1.12.2.compliant.gff3"))
	if err != nil {
		t.Fatal(err)
	}
	genome := mustGenome(t, "synthetic", fasta)
	reduced, err := ReduceBaktaJSON(annotation, genome)
	if err != nil {
		t.Fatalf("ReduceBaktaJSON() error = %v", err)
	}
	for input, json := range map[string][]byte{"original": annotation, "reduced": reduced.ReducedJSON} {
		t.Run(input, func(t *testing.T) {
			var warnings []string
			got, err := BaktaGFF3WithOptions(json, genome, BaktaGFF3Options{
				Compliant: true,
				Warn:      func(message string) { warnings = append(warnings, message) },
			})
			if err != nil {
				t.Fatalf("BaktaGFF3WithOptions() error = %v", err)
			}
			if got := stripBakpackGFF3Header(got); !bytes.Equal(got, want) {
				t.Fatalf("compliant GFF3 differs from Bakta: %s", firstDiff(got, want))
			}
			joined := strings.Join(warnings, "\n")
			for _, wantWarning := range []string{
				"left out 1 contig(s) shorter than 200 bp and their 1 feature(s), as Bakta --compliant does: contig_3",
				"1 contig ID(s) are not INSDC compliant",
				"contig_4_with_a_longer_name",
				"locus tag prefix(es) are not INSDC compliant (3-12 upper case letters and digits, starting with a letter): syn",
			} {
				if !strings.Contains(joined, wantWarning) {
					t.Errorf("warnings missing %q:\n%s", wantWarning, joined)
				}
			}
		})
	}

	// Default output keeps everything as annotated.
	got, err := BaktaGFF3(annotation, genome)
	if err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{"##sequence-region contig_3 1 150", "product=Uncharacterized protein (fragment"} {
		if !bytes.Contains(got, []byte(kept)) {
			t.Errorf("default GFF3 missing %q", kept)
		}
	}
}

// TestReviseProductINSDC compares with Bakta's revise_product_insdc, whose
// output for these names was taken from Bakta 1.12.2.
func TestReviseProductINSDC(t *testing.T) {
	for product, want := range map[string]string{
		"Uncharacterized protein YbaB":                             "putative protein YbaB",
		"uncharacterised membrane protein":                         "putative membrane protein",
		"UNCHARACTERIZED protein":                                  "putative protein",
		"Putative uncharacterized protein, uncharacterised domain": "Putative putative protein, putative domain",
		"Protein (abcD":                                            "Protein abcD",
		"Protein abcD)":                                            "Protein abcD",
		"Balanced (abcD) protein":                                  "Balanced (abcD) protein",
		"Iron [Fe-S cluster protein":                               "Iron Fe-S cluster protein",
		"Iron [Fe-S] cluster (protein":                             "Iron [Fe-S] cluster protein",
		"Nested ((one) two":                                        "Nested one two",
		"hypothetical protein":                                     "hypothetical protein",
		"DNA-binding protein HU-beta":                              "DNA-binding protein HU-beta",
		"Uncharacterized protein (fragment":                        "putative protein fragment",
		"Characterized protein":                                    "Characterized protein",
		"":                                                         "",
		"[[unbalanced]":                                            "unbalanced",
		"(x)(y))":                                                  "xy",
	} {
		got := reviseProductINSDC(product)
		if got != want {
			t.Errorf("reviseProductINSDC(%q) = %q, want %q", product, got, want)
		}
		// Revising a revised name changes nothing, so JSON from a compliant
		// run renders exactly as Bakta wrote it.
		if again := reviseProductINSDC(got); again != got {
			t.Errorf("reviseProductINSDC(%q) = %q, want it unchanged", got, again)
		}
	}
}

func TestPyFloatRepr(t *testing.T) {
	for value, want := range map[float64]string{
		0:                   "0.0",
		1:                   "1.0",
		0.875:               "0.875",
		1.2e-15:             "1.2e-15",
		2e-05:               "2e-05",
		0.0001:              "0.0001",
		123456789012345.0:   "123456789012345.0",
		1234567890123456.0:  "1234567890123456.0",
		12345678901234567.0: "1.2345678901234568e+16",
		-3.5e-300:           "-3.5e-300",
		1e16:                "1e+16",
	} {
		if got := pyFloatRepr(value); got != want {
			t.Errorf("pyFloatRepr(%v) = %q, want %q", value, got, want)
		}
	}
}

// TestBaktaGFF3OldLayoutMatchesBakta checks JSON in the Bakta < 1.10 layout
// against GFF3 written for it by Bakta 1.12.2, as bakta_io does for old JSON.
func TestBaktaGFF3OldLayoutMatchesBakta(t *testing.T) {
	annotation, err := os.ReadFile(filepath.Join("testdata", "bakta", "synthetic.v1.9.bakta.json"))
	if err != nil {
		t.Fatal(err)
	}
	fasta, err := os.ReadFile(filepath.Join("testdata", "bakta", "synthetic.fa"))
	if err != nil {
		t.Fatal(err)
	}
	genome := mustGenome(t, "synthetic", fasta)
	for _, tt := range []struct {
		compliant bool
		want      string
	}{
		{want: "synthetic.v1.9.bakta-1.12.2.gff3"},
		{compliant: true, want: "synthetic.v1.9.bakta-1.12.2.compliant.gff3"},
	} {
		want, err := os.ReadFile(filepath.Join("testdata", "bakta", tt.want))
		if err != nil {
			t.Fatal(err)
		}
		got, err := BaktaGFF3WithOptions(annotation, genome, BaktaGFF3Options{Compliant: tt.compliant})
		if err != nil {
			t.Fatalf("BaktaGFF3WithOptions(compliant=%v) error = %v", tt.compliant, err)
		}
		if got := stripBakpackGFF3Header(got); !bytes.Equal(got, want) {
			t.Fatalf("compliant=%v: GFF3 differs from Bakta: %s", tt.compliant, firstDiff(got, want))
		}
	}
}

func TestGFFFeatureIDFallsBackToDistinctValuesWithoutLocusOrID(t *testing.T) {
	featureA := map[string]any{"type": "gap", "contig": "contig1", "start": 1, "stop": 4}
	featureB := map[string]any{"type": "gap", "contig": "contig1", "start": 5, "stop": 9}

	idA := gffFeatureID(featureA)
	idB := gffFeatureID(featureB)

	if idA == "." || idB == "." {
		t.Fatalf("gffFeatureID() = %q, %q, want no fallback to the ambiguous constant \".\"", idA, idB)
	}
	if idA == idB {
		t.Fatalf("gffFeatureID() returned the same ID %q for two different features", idA)
	}
}

func TestBaktaGFF3FromOriginalAndReducedJSON(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	original := toyBaktaJSON("sample1", "gene one")
	want := []byte(strings.Join([]string{
		"##gff-version 3",
		"##feature-ontology https://github.com/The-Sequence-Ontology/SO-Ontologies/blob/v3.1/so.obo",
		"# Generated by bakpack " + buildinfo.Version,
		"# Annotated with Bakta",
		"# DOI: 10.1099/mgen.0.000685",
		"# URL: github.com/oschwengers/bakta",
		"##sequence-region contig1 1 9",
		"contig1\tBakta\tregion\t1\t9\t.\t+\t.\tID=contig1;Name=contig1",
		"contig1\tPyrodigal\tCDS\t1\t9\t.\t+\t0\tID=toy_00001;Name=gene one;locus_tag=toy_00001;product=gene one",
		"##FASTA",
		">contig1",
		"ATGAAATAA",
		"",
	}, "\n"))

	got, err := BaktaGFF3(original, genome)
	if err != nil {
		t.Fatalf("BaktaGFF3(original) error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("BaktaGFF3(original) =\n%s\nwant\n%s", got, want)
	}

	reduced, err := ReduceBaktaJSON(original, genome)
	if err != nil {
		t.Fatal(err)
	}
	got, err = BaktaGFF3(reduced.ReducedJSON, genome)
	if err != nil {
		t.Fatalf("BaktaGFF3(reduced) error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("BaktaGFF3(reduced) =\n%s\nwant\n%s", got, want)
	}

	wantAnnotationOnly := []byte(strings.Join([]string{
		"##gff-version 3",
		"##feature-ontology https://github.com/The-Sequence-Ontology/SO-Ontologies/blob/v3.1/so.obo",
		"# Generated by bakpack " + buildinfo.Version,
		"# Annotated with Bakta",
		"# DOI: 10.1099/mgen.0.000685",
		"# URL: github.com/oschwengers/bakta",
		"##sequence-region contig1 1 9",
		"contig1\tBakta\tregion\t1\t9\t.\t+\t.\tID=contig1;Name=contig1",
		"contig1\tPyrodigal\tCDS\t1\t9\t.\t+\t0\tID=toy_00001;Name=gene one;locus_tag=toy_00001;product=gene one",
		"",
	}, "\n"))
	got, err = BaktaGFF3WithOptions(reduced.ReducedJSON, genome, BaktaGFF3Options{AnnotationOnly: true})
	if err != nil {
		t.Fatalf("BaktaGFF3WithOptions(reduced, AnnotationOnly) error = %v", err)
	}
	if !bytes.Equal(got, wantAnnotationOnly) {
		t.Fatalf("BaktaGFF3WithOptions(reduced, AnnotationOnly) =\n%s\nwant\n%s", got, wantAnnotationOnly)
	}

	got, err = BaktaGFF3WithOptions(original, genome, BaktaGFF3Options{BakpackVersion: "v9.8.7"})
	if err != nil {
		t.Fatalf("BaktaGFF3WithOptions(original, BakpackVersion) error = %v", err)
	}
	if !bytes.Contains(got, []byte("# Generated by bakpack v9.8.7\n")) {
		t.Fatalf("BaktaGFF3WithOptions(original, BakpackVersion) missing version override: %s", got)
	}
}

func TestBaktaGFF3MatchesProvidedBaktaOutput(t *testing.T) {
	base := "/Users/mh46/Work/Compress_bakta"
	annotationDir := filepath.Join(base, "atb.bakta.r0.2.batch.10")
	matches, err := filepath.Glob(filepath.Join(annotationDir, "*.bakta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Skipf("local Bakta parity fixtures not available under %s", annotationDir)
	}
	for _, annotationPath := range matches {
		sample := strings.TrimSuffix(filepath.Base(annotationPath), ".bakta.json")
		t.Run(sample, func(t *testing.T) {
			genomePath := filepath.Join(base, "atb.assembly.r0.2.batch.10", sample+".fa")
			gff3Path := filepath.Join(base, "atb.bakta.r0.2.batch.10.gff3_files", sample+".gff3")
			for _, path := range []string{genomePath, gff3Path} {
				if _, err := os.Stat(path); err != nil {
					t.Skipf("local Bakta parity fixture not available: %s", path)
				}
			}
			annotation, err := os.ReadFile(annotationPath)
			if err != nil {
				t.Fatal(err)
			}
			genomeBytes, err := os.ReadFile(genomePath)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(gff3Path)
			if err != nil {
				t.Fatal(err)
			}
			genome, err := ReadGenome(sample, sample+".fa", genomeBytes)
			if err != nil {
				t.Fatal(err)
			}
			got, err := BaktaGFF3(annotation, genome)
			if err != nil {
				t.Fatalf("BaktaGFF3() error = %v", err)
			}
			gotWithoutBakpackHeader := stripBakpackGFF3Header(got)
			if !bytes.Equal(gotWithoutBakpackHeader, want) {
				t.Fatalf("BaktaGFF3() differs from Bakta GFF3 fixture after stripping bakpack header: %s", firstDiff(gotWithoutBakpackHeader, want))
			}
		})
	}
}

func TestBaktaGFF3FeatureTypesCoverValidationIndexes(t *testing.T) {
	roots := []string{
		"/Users/mh46/Work/Compress_bakta/incr_202408_bakpack_validation_v9_10",
		"/Users/mh46/Work/Compress_bakta/incr_202408_bakpack_validation_v9_10_remote",
	}
	var indexPaths []string
	for _, root := range roots {
		matches, err := filepath.Glob(filepath.Join(root, "work", "batch_*", "index.json"))
		if err != nil {
			t.Fatal(err)
		}
		indexPaths = append(indexPaths, matches...)
	}
	if len(indexPaths) == 0 {
		t.Skip("local bakpack validation indexes not available")
	}

	observed := map[string]bool{}
	unsupported := map[string][]string{}
	totalSamples := 0
	for _, path := range indexPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		root, err := DecodeJSON(data)
		if err != nil {
			t.Fatal(err)
		}
		if obj, ok := root.(map[string]any); ok {
			totalSamples += len(mapSlice(obj["samples"]))
		}
		walkIndexTypeValues(root, func(featureType string) {
			observed[featureType] = true
			if !gffFeatureTypeSupported(featureType) {
				unsupported[featureType] = append(unsupported[featureType], path)
			}
		})
	}
	if len(observed) == 0 {
		t.Fatalf("no feature type values found in %d validation indexes", len(indexPaths))
	}
	if len(unsupported) != 0 {
		t.Fatalf("validation indexes contain unsupported GFF3 feature types across %d samples: %#v", totalSamples, unsupported)
	}
}

func TestSupportedBaktaFeatureTypes(t *testing.T) {
	want := []string{"cds", "crispr", "gap", "ncRNA", "ncRNA-region", "oriC", "oriT", "oriV", "rRNA", "sorf", "tRNA", "tmRNA"}
	got := SupportedBaktaFeatureTypes()
	if !slices.Equal(got, want) {
		t.Fatalf("SupportedBaktaFeatureTypes() = %#v, want %#v", got, want)
	}
}

func TestBaktaGFF3ErrorsOnUnsupportedFeatureType(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	annotation := bytes.Replace(toyBaktaJSON("sample1", "gene one"), []byte(`"type": "cds"`), []byte(`"type": "new-feature"`), 1)

	_, err := BaktaGFF3(annotation, genome)
	if err == nil || !strings.Contains(err.Error(), `unsupported Bakta feature type "new-feature"`) {
		t.Fatalf("BaktaGFF3() error = %v, want unsupported feature type error", err)
	}
}

func firstDiff(got, want []byte) string {
	limit := len(got)
	if len(want) < limit {
		limit = len(want)
	}
	for i := 0; i < limit; i++ {
		if got[i] != want[i] {
			return fmt.Sprintf("offset %d got %q want %q", i, got[i], want[i])
		}
	}
	return fmt.Sprintf("length got %d want %d", len(got), len(want))
}

func stripBakpackGFF3Header(data []byte) []byte {
	line := []byte("# Generated by bakpack " + buildinfo.Version + "\n")
	return bytes.Replace(data, line, nil, 1)
}

func gffFeatureTypeSupported(featureType string) bool {
	return isSupportedBaktaFeatureType(featureType)
}

func walkIndexTypeValues(value any, fn func(string)) {
	switch v := value.(type) {
	case map[string]any:
		if field, _ := v["field"].(string); field == "type" {
			for _, item := range stringSlice(v["values"]) {
				fn(item)
			}
		}
		for _, child := range v {
			walkIndexTypeValues(child, fn)
		}
	case []any:
		for _, child := range v {
			walkIndexTypeValues(child, fn)
		}
	}
}
