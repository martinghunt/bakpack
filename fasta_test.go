package bakpack

import (
	"strings"
	"testing"

	"github.com/martinghunt/faqt/seqio"
)

func TestGenomeFASTABytesReturnsWrappedSequence(t *testing.T) {
	genome := Genome{Contigs: []seqio.SeqRecord{{Name: "contig1", Seq: []byte("ACGTACGTACGT")}}}

	got, err := genome.FASTABytes(4)
	if err != nil {
		t.Fatalf("FASTABytes() error = %v", err)
	}
	want := ">contig1\nACGT\nACGT\nACGT\n"
	if string(got) != want {
		t.Fatalf("FASTABytes() = %q, want %q", got, want)
	}
	if !strings.HasPrefix(string(got), ">contig1") {
		t.Fatalf("FASTABytes() = %q, want it to start with a FASTA header", got)
	}
}

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
