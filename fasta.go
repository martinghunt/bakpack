package bakpack

import (
	"bytes"
	"fmt"
	"io"
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

func (g Genome) Contig(name string) ([]byte, bool) {
	if g.byName == nil {
		g.byName = map[string][]byte{}
		for _, contig := range g.Contigs {
			g.byName[contig.Name] = contig.Seq
		}
	}
	seqBytes, ok := g.byName[name]
	return seqBytes, ok
}

func (g Genome) FASTABytes(wrap int) []byte {
	if wrap <= 0 {
		wrap = 80
	}
	var buf bytes.Buffer
	writer := seqio.NewFASTAWriter(&buf, seqio.WithWrap(wrap))
	for _, contig := range g.Contigs {
		_ = writer.Write(&contig)
	}
	_ = writer.Close()
	return buf.Bytes()
}

func FeatureNT(feature map[string]any, genome Genome) (string, bool) {
	contigName, ok := feature["contig"].(string)
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
	if !ok || start < 1 || stop < start || stop > len(contig) {
		return "", false
	}
	nt := append([]byte(nil), contig[start-1:stop]...)
	if strand, _ := feature["strand"].(string); strand == "-" {
		nt = seq.ReverseComplement(nt)
	}
	return strings.ToUpper(string(nt)), true
}
