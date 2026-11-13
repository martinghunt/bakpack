package bakpack

import (
	"os"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustGenome(t *testing.T, sample string, data []byte) Genome {
	t.Helper()
	genome, err := ReadGenome(sample, sample+".fa", data)
	if err != nil {
		t.Fatal(err)
	}
	return genome
}

func toyFASTA(sample string) []byte {
	return []byte(">contig1 " + sample + "\nATGAAATAA\n")
}

func toyBaktaJSON(sample, product string) []byte {
	_ = sample
	return []byte(`{
  "genome": {
    "translation_table": 11
  },
  "sequences": [
    {
      "id": "contig1",
      "description": "toy contig",
      "length": 9,
      "sequence": "ATGAAATAA"
    }
  ],
  "stats": {
    "no_sequences": 1,
    "size": 9,
    "gc": 33.333333333333336,
    "n_ratio": 0.0,
    "n50": 9
  },
  "features": [
    {
      "type": "cds",
      "contig": "contig1",
      "start": 1,
      "stop": 9,
      "strand": "+",
      "product": "` + product + `",
      "nt": "ATGAAATAA",
      "aa": "MK",
      "aa_hexdigest": "fbd1e7ba9564863b88d5c43cb833afaf",
      "start_type": "ATG",
      "id": "toy_00001"
    }
  ]
}
`)
}

// toyBaktaJSONv110 is toyBaktaJSON in the Bakta >= 1.10.0 layout, which keys
// sequences[] nucleotides as "nt" and feature sequence IDs as "sequence".
func toyBaktaJSONv110(sample, product string) []byte {
	out := string(toyBaktaJSON(sample, product))
	out = strings.Replace(out, `"sequence": "ATGAAATAA"`, `"nt": "ATGAAATAA"`, 1)
	out = strings.Replace(out, `"contig": "contig1"`, `"sequence": "contig1"`, 1)
	return []byte(out)
}

// renamedContigFASTA and renamedContigBaktaJSON mimic Bakta run without
// --keep-contig-headers: FASTA record "1" is renamed "contig_1" and kept as
// orig_id. The JSON also has a partial stats object and a CDS truncated at
// the contig edge whose first codon (TTG) is translated literally as L.
func renamedContigFASTA() []byte {
	return []byte(">1 length=18\nTTGAAATAAATGAAATAA\n")
}

func renamedContigBaktaJSON() []byte {
	return []byte(`{
  "genome": {"translation_table": 11},
  "stats": {"size": 18},
  "sequences": [
    {"id": "contig_1", "orig_id": "1", "nt": "TTGAAATAAATGAAATAA", "length": 18}
  ],
  "features": [
    {"type": "cds", "sequence": "contig_1", "start": 1, "stop": 9, "strand": "+", "product": "edge protein", "truncated": "5-prime", "start_type": "Edge", "nt": "TTGAAATAA", "aa": "LK", "aa_hexdigest": "2c16b194a0d9037002e9ef6580925697", "locus": "x_1"},
    {"type": "cds", "sequence": "contig_1", "start": 10, "stop": 18, "strand": "+", "product": "gene two", "nt": "ATGAAATAA", "aa": "MK", "aa_hexdigest": "fbd1e7ba9564863b88d5c43cb833afaf", "start_type": "ATG", "locus": "x_2"}
  ]
}
`)
}
