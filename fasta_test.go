package bakpack

import (
	"testing"

	"github.com/martinghunt/faqt/seqio"
)

func TestGenomeContigCachesLookupMapAcrossCalls(t *testing.T) {
	genome := Genome{Contigs: []seqio.SeqRecord{{Name: "contig1", Seq: []byte("ACGT")}}}
	if genome.byName != nil {
		t.Fatal("Genome{} unexpectedly has a pre-populated byName cache")
	}

	seqBytes, ok := genome.Contig("contig1")
	if !ok || string(seqBytes) != "ACGT" {
		t.Fatalf("Contig(%q) = %q, %v, want %q, true", "contig1", seqBytes, ok, "ACGT")
	}
	if genome.byName == nil {
		t.Fatal("Contig() did not persist its lazily-built cache on the Genome")
	}

	seqBytes, ok = genome.Contig("contig1")
	if !ok || string(seqBytes) != "ACGT" {
		t.Fatalf("second Contig(%q) = %q, %v, want %q, true", "contig1", seqBytes, ok, "ACGT")
	}
}
