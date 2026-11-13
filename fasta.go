package bakpack

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"strings"

	"github.com/martinghunt/faqt/seq"
	"github.com/martinghunt/faqt/seqio"
)

type Genome struct {
	SampleID string
	Filename string
	Contigs  []seqio.SeqRecord
	byName   map[string][]byte
}

func ReadGenome(sampleID, filename string, data []byte) (Genome, error) {
	reader, err := seqio.OpenReader(bytes.NewReader(data))
	if err != nil {
		return Genome{}, err
	}
	if closer, ok := reader.(io.Closer); ok {
		defer closer.Close()
	}
	genome := Genome{SampleID: sampleID, Filename: filename, byName: map[string][]byte{}}
	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Genome{}, err
		}
		contig := seqio.SeqRecord{
			Name:        rec.Name,
			Description: rec.Description,
			Seq:         seq.NormalizeDNA(rec.Seq),
		}
		genome.Contigs = append(genome.Contigs, contig)
		genome.byName[contig.Name] = contig.Seq
	}
	if len(genome.Contigs) == 0 {
		return Genome{}, fmt.Errorf("no FASTA records in %s", filename)
	}
	return genome, nil
}

// Contig has a pointer receiver so the lazily-built byName cache actually
// persists on the caller's Genome instead of being discarded with a copy;
// ReadGenome always populates byName up front, but a Genome built directly
// (e.g. Genome{Contigs: ...}) relies on this cache being retained across calls.
func (g *Genome) Contig(name string) ([]byte, bool) {
	if g.byName == nil {
		g.byName = map[string][]byte{}
		for _, contig := range g.Contigs {
			g.byName[contig.Name] = contig.Seq
		}
	}
	seqBytes, ok := g.byName[name]
	return seqBytes, ok
}

// withBaktaContigAliases returns genome with each Bakta sequences[] ID that
// is not a FASTA record name mapped to the record named by its orig_id. Unless
// run with --keep-contig-headers, Bakta renames contigs (e.g. "1" to
// "contig_1") and keeps the FASTA name in orig_id.
func withBaktaContigAliases(data map[string]any, genome Genome) Genome {
	var byName map[string][]byte
	for _, entry := range baktaSequences(data) {
		id, _ := entry["id"].(string)
		origID, _ := entry["orig_id"].(string)
		if id == "" || origID == "" || id == origID {
			continue
		}
		if _, ok := genome.Contig(id); ok {
			continue
		}
		contig, ok := genome.Contig(origID)
		if !ok {
			continue
		}
		if byName == nil {
			byName = maps.Clone(genome.byName)
		}
		byName[id] = contig
	}
	if byName != nil {
		genome.byName = byName
	}
	return genome
}

// UnmatchedSequencesWarning formats a warning for ReduceResult.UnmatchedSequences.
func UnmatchedSequencesWarning(sample string, unmatched []string) string {
	shown := unmatched
	if len(shown) > 3 {
		shown = shown[:3]
	}
	more := ""
	if len(unmatched) > len(shown) {
		more = ", ..."
	}
	return fmt.Sprintf("warning: %s: %d sequence(s) not found in genome FASTA by id or orig_id, or sequence differs (%s%s); their sequences and features were not reduced",
		sample, len(unmatched), strings.Join(shown, ", "), more)
}

func (g Genome) FASTABytes(wrap int) ([]byte, error) {
	if wrap <= 0 {
		wrap = 80
	}
	var buf bytes.Buffer
	writer := seqio.NewFASTAWriter(&buf, seqio.WithWrap(wrap))
	for _, contig := range g.Contigs {
		if err := writer.Write(&contig); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func FeatureNT(feature map[string]any, genome Genome) (string, bool) {
	contigName, ok := featureContig(feature)
	if !ok {
		return "", false
	}
	start, ok := jsonInt(feature["start"])
	if !ok {
		return "", false
	}
	stop, ok := jsonInt(feature["stop"])
	if !ok {
		return "", false
	}
	contig, ok := genome.Contig(contigName)
	if !ok || start < 1 || stop < 1 {
		return "", false
	}
	var nt []byte
	if start <= stop {
		if stop > len(contig) {
			return "", false
		}
		nt = append([]byte(nil), contig[start-1:stop]...)
	} else {
		if start > len(contig) {
			return "", false
		}
		nt = append([]byte(nil), contig[start-1:]...)
		nt = append(nt, contig[:stop]...)
	}
	strand, _ := feature["strand"].(string)
	if strand == "-" {
		nt = seq.ReverseComplement(nt)
	} else if strand != "" && strand != "+" && strand != "?" {
		return "", false
	}
	return strings.ToUpper(string(nt)), true
}
