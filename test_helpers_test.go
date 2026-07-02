package bakpack

import (
	"os"
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
