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
// Bakta 1.12.2's own writer; see testdata/gff3/README.md.
func TestBaktaGFF3MatchesBakta112(t *testing.T) {
	annotation, err := os.ReadFile(filepath.Join("testdata", "gff3", "synthetic.bakta.json"))
	if err != nil {
		t.Fatal(err)
	}
	fasta, err := os.ReadFile(filepath.Join("testdata", "gff3", "synthetic.fa"))
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
		want, err := os.ReadFile(filepath.Join("testdata", "gff3", tt.want))
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

func TestBaktaGFF3StyleFollowsBaktaVersion(t *testing.T) {
	// A truncated CDS is marked pseudo=True from Bakta 1.10, but not before.
	withVersion := func(json []byte, version string) []byte {
		out := strings.Replace(string(json), `"translation_table": 11`, `"translation_table": 11`+"\n  },\n  \"version\": {\n    \"bakta\": \""+version+`"`, 1)
		return []byte(strings.Replace(out, `"start_type": "ATG",`, `"start_type": "ATG", "truncated": "3-prime",`, 1))
	}
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	for _, tt := range []struct {
		name       string
		annotation []byte
		wantPseudo bool
	}{
		{name: "Bakta 1.9.4", annotation: withVersion(toyBaktaJSON("sample1", "gene one"), "1.9.4"), wantPseudo: false},
		{name: "Bakta 1.10.0", annotation: withVersion(toyBaktaJSONv110("sample1", "gene one"), "1.10.0"), wantPseudo: true},
		{name: "Bakta 1.12.0 fork", annotation: withVersion(toyBaktaJSONv110("sample1", "gene one"), "1.12.0+pannotator.5"), wantPseudo: true},
		{name: "no version, older layout", annotation: toyBaktaJSON("sample1", "gene one"), wantPseudo: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			annotation := tt.annotation
			if tt.name == "no version, older layout" {
				annotation = []byte(strings.Replace(string(annotation), `"start_type": "ATG",`, `"start_type": "ATG", "truncated": "3-prime",`, 1))
			}
			gff, err := BaktaGFF3(annotation, genome)
			if err != nil {
				t.Fatalf("BaktaGFF3() error = %v", err)
			}
			if got := bytes.Contains(gff, []byte(";pseudo=True")); got != tt.wantPseudo {
				t.Fatalf("pseudo=True present = %v, want %v:\n%s", got, tt.wantPseudo, gff)
			}
		})
	}

	_, err := BaktaGFF3WithOptions(withVersion(toyBaktaJSON("sample1", "gene one"), "1.9.4"), genome, BaktaGFF3Options{Compliant: true})
	if err == nil || !strings.Contains(err.Error(), "Bakta 1.10 or later") {
		t.Fatalf("compliant GFF3 for Bakta 1.9.4 JSON error = %v, want Bakta 1.10 or later", err)
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
		"contig1\tPyrodigal\tCDS\t1\t9\t.\t+\t0\tID=toy_00001;Name=gene one;product=gene one",
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
		"contig1\tPyrodigal\tCDS\t1\t9\t.\t+\t0\tID=toy_00001;Name=gene one;product=gene one",
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
