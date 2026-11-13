package bakpack

import (
	"bytes"
	"fmt"

	"github.com/martinghunt/bakpack/internal/buildinfo"
)

// BaktaGFF3Options configures Bakta JSON to GFF3 rendering.
type BaktaGFF3Options struct {
	// AnnotationOnly omits the terminal ##FASTA section containing contig
	// sequences.
	AnnotationOnly bool
	// BakpackVersion overrides the version written in the generated-by header.
	// When empty, the build-time bakpack version is used.
	BakpackVersion string
	// Compliant writes GFF3 as Bakta's --compliant (INSDC) mode does. It also
	// applies the changes Bakta's --compliant mode makes while annotating, so
	// the output is compliant even when Bakta was run without --compliant.
	Compliant bool
	// Warn, if set, receives problems found in compliant mode that bakpack
	// cannot fix, and contigs it leaves out.
	Warn func(message string)
}

// BaktaGFF3 renders a Bakta JSON annotation as GFF3, as current Bakta writes
// it for that JSON (see docs/bakta-gff3-format.md). The input may be either
// original JSON or bakpack reduced JSON; reduced input is restored before
// rendering.
func BaktaGFF3(annotation []byte, genome Genome) ([]byte, error) {
	return BaktaGFF3WithOptions(annotation, genome, BaktaGFF3Options{})
}

// BaktaGFF3WithOptions renders a Bakta JSON annotation as GFF3 with explicit
// output options.
func BaktaGFF3WithOptions(annotation []byte, genome Genome, opts BaktaGFF3Options) ([]byte, error) {
	return ConvertBaktaJSON(annotation, genome, ConvertOptions{
		Format:         FormatGFF3,
		Compliant:      opts.Compliant,
		AnnotationOnly: opts.AnnotationOnly,
		BakpackVersion: opts.BakpackVersion,
		Warn:           opts.Warn,
	})
}

func (opts BaktaGFF3Options) bakpackVersion() string {
	if opts.BakpackVersion != "" {
		return opts.BakpackVersion
	}
	return buildinfo.Version
}

// checkConversionInput rejects JSON that cannot be converted: sequences
// without a positive length, and features without a sequence ID or with
// coordinates below 1. A start beyond the stop is allowed; it marks a feature
// wrapping round the origin of a circular sequence.
func checkConversionInput(data map[string]any) error {
	for _, seq := range mapSlice(data["sequences"]) {
		if length, ok := jsonInt(seq["length"]); !ok || length < 1 {
			return fmt.Errorf("sequence %q has invalid length %v", seq["id"], seq["length"])
		}
	}
	for _, feature := range mapSlice(data["features"]) {
		contig, _ := featureContig(feature)
		start, startOK := jsonInt(feature["start"])
		stop, stopOK := jsonInt(feature["stop"])
		if contig == "" || !startOK || !stopOK {
			return fmt.Errorf("feature %s is missing contig, start, or stop", gffFeatureID(feature))
		}
		if start < 1 || stop < 1 {
			return fmt.Errorf("feature %s has invalid coordinates %d-%d", gffFeatureID(feature), start, stop)
		}
	}
	return nil
}

func gffFeaturesByContig(data map[string]any) (map[string][]map[string]any, []string) {
	featuresByContig := map[string][]map[string]any{}
	seen := map[string]bool{}
	var order []string
	for _, feature := range mapSlice(data["features"]) {
		contig, _ := featureContig(feature)
		if contig == "" {
			contig = "."
		}
		if !seen[contig] {
			seen[contig] = true
			order = append(order, contig)
		}
		featuresByContig[contig] = append(featuresByContig[contig], feature)
	}
	return featuresByContig, order
}

func gffFeatureID(feature map[string]any) string {
	if locus, _ := feature["locus"].(string); locus != "" {
		return locus
	}
	if id, _ := feature["id"].(string); id != "" {
		return id
	}
	// Neither field is set: fall back to a value derived from the feature's
	// own coordinates instead of a constant, so two such features don't end
	// up sharing the same (invalid, ambiguous) GFF3 ID.
	featureType, _ := feature["type"].(string)
	contig, _ := featureContig(feature)
	start, _ := jsonInt(feature["start"])
	stop, _ := jsonInt(feature["stop"])
	return fmt.Sprintf("%s_%s_%d_%d", contig, featureType, start, stop)
}

func writeWrappedSequence(buf *bytes.Buffer, sequence string, width int) {
	if width <= 0 {
		width = 60
	}
	for len(sequence) > width {
		buf.WriteString(sequence[:width])
		buf.WriteByte('\n')
		sequence = sequence[width:]
	}
	if sequence != "" {
		buf.WriteString(sequence)
		buf.WriteByte('\n')
	}
}

func mapSlice(value any) []map[string]any {
	raw, _ := value.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if ok {
			out = append(out, obj)
		}
	}
	return out
}

func stringSlice(value any) []string {
	raw, _ := value.([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		str, ok := item.(string)
		if ok {
			out = append(out, str)
		}
	}
	return out
}
