package bakpack

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	dsnetbzip2 "github.com/dsnet/compress/bzip2"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// BatchOptions configures ReduceBatch, RestoreBatch and GFF3Batch, which
// process every sample in a pair of annotation and genome sources.
type BatchOptions struct {
	Annotations FileSource
	Genomes     FileSource
	// Order optionally sets the output order. The default is genome source
	// order.
	Order []string
	// OutputPath is a tar file when it ends in a tar suffix (.tar, .tar.gz,
	// .tgz, .tar.bz2, .tbz2, .tar.xz, .txz, .tar.zst or .tzst), otherwise a
	// directory, created if needed.
	OutputPath string
	// XZThreads is passed to xz as -T when writing a .tar.xz. 0 means 1.
	XZThreads int
	// SpoolCompression controls temporary files holding tar members until
	// they are paired, as BuildOptions.AnnotationSpoolCompression does.
	SpoolCompression string
	// Warnings, if set, receives ReduceBatch's unmatched sequence warnings and
	// GFF3Batch's compliant mode warnings.
	Warnings io.Writer
	// GFF3AnnotationOnly makes GFF3Batch omit the ##FASTA section.
	GFF3AnnotationOnly bool
	// GFF3Compliant makes GFF3Batch write GFF3 as Bakta's --compliant mode
	// does, for samples annotated with Bakta 1.10 or later.
	GFF3Compliant bool
}

// ReduceBatch writes reduced JSON for each sample, named
// SAMPLE.reduced.bakta.json as extract names it.
func ReduceBatch(ctx context.Context, opts BatchOptions) error {
	return runBatch(ctx, opts, func(sample string, annotation []byte, genome Genome) (string, []byte, error) {
		result, err := ReduceBaktaJSON(annotation, genome)
		if err != nil {
			return "", nil, err
		}
		if opts.Warnings != nil && len(result.UnmatchedSequences) != 0 {
			fmt.Fprintln(opts.Warnings, UnmatchedSequencesWarning(sample, result.UnmatchedSequences))
		}
		return sample + ".reduced.bakta.json", result.ReducedJSON, nil
	})
}

// RestoreBatch writes the original Bakta JSON for each reduced JSON sample,
// named SAMPLE.bakta.json.
func RestoreBatch(ctx context.Context, opts BatchOptions) error {
	return runBatch(ctx, opts, func(sample string, annotation []byte, genome Genome) (string, []byte, error) {
		result, err := RestoreBaktaJSON(annotation, genome)
		if err != nil {
			return "", nil, err
		}
		return sample + ".bakta.json", result.OriginalJSON, nil
	})
}

// GFF3Batch writes GFF3 for each original or reduced Bakta JSON sample,
// named SAMPLE.gff3.
func GFF3Batch(ctx context.Context, opts BatchOptions) error {
	return runBatch(ctx, opts, func(sample string, annotation []byte, genome Genome) (string, []byte, error) {
		gff3, err := BaktaGFF3WithOptions(annotation, genome, BaktaGFF3Options{
			AnnotationOnly: opts.GFF3AnnotationOnly,
			Compliant:      opts.GFF3Compliant,
			Warn:           sampleWarner(opts.Warnings, sample),
		})
		if err != nil {
			return "", nil, err
		}
		return sample + ".gff3", gff3, nil
	})
}

// sampleWarner returns a function writing warnings about sample to w, or nil
// if w is nil.
func sampleWarner(w io.Writer, sample string) func(string) {
	if w == nil {
		return nil
	}
	return func(message string) {
		fmt.Fprintf(w, "warning: %s: %s\n", sample, message)
	}
}

// runBatch pairs the sources and writes one output file per sample. It stops
// at the first failing sample; a tar output is then not created.
func runBatch(ctx context.Context, opts BatchOptions, process func(sample string, annotation []byte, genome Genome) (string, []byte, error)) error {
	if opts.Annotations == nil {
		return fmt.Errorf("annotation source is required")
	}
	if opts.Genomes == nil {
		return fmt.Errorf("genome source is required")
	}
	if opts.OutputPath == "" {
		return fmt.Errorf("output path is required")
	}
	out, err := openBatchOutput(opts)
	if err != nil {
		return err
	}
	pairing := samplePairing{
		annotations:      opts.Annotations,
		genomes:          opts.Genomes,
		order:            opts.Order,
		outputPath:       opts.OutputPath,
		spoolCompression: opts.SpoolCompression,
	}
	err = pairing.run(ctx, func(sample string, annotation, genomeRecord FileRecord) error {
		genome, err := ReadGenome(sample, genomeRecord.Name, genomeRecord.Bytes)
		if err != nil {
			return fmt.Errorf("%s: %w", sample, err)
		}
		name, data, err := process(sample, annotation.Bytes, genome)
		if err != nil {
			return fmt.Errorf("%s: %w", sample, err)
		}
		return out.write(name, data)
	})
	if err != nil {
		out.abort()
		return err
	}
	return out.close()
}

// batchOutput receives one file per sample.
type batchOutput interface {
	write(name string, data []byte) error
	// close finishes the output; abort discards a partial tar.
	close() error
	abort()
}

func openBatchOutput(opts BatchOptions) (batchOutput, error) {
	if isTarPath(opts.OutputPath) {
		return newTarBatchOutput(opts.OutputPath, tarCompressionFromPath(opts.OutputPath), opts.XZThreads)
	}
	if err := os.MkdirAll(opts.OutputPath, 0o755); err != nil {
		return nil, err
	}
	return dirBatchOutput{dir: opts.OutputPath}, nil
}

type dirBatchOutput struct {
	dir string
}

func (o dirBatchOutput) write(name string, data []byte) error {
	path, err := safeExtractPath(o.dir, name)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (o dirBatchOutput) close() error { return nil }

func (o dirBatchOutput) abort() {}

// tarBatchOutput writes a tar to a temporary file beside path and renames it
// into place when complete, so a failed run leaves no partial tar.
type tarBatchOutput struct {
	path       string
	file       *os.File
	compressor io.WriteCloser
	tw         *tar.Writer
	modTime    time.Time
	// wait, if set, waits for the external xz process to finish.
	wait func() error
}

func newTarBatchOutput(path, compression string, xzThreads int) (*tarBatchOutput, error) {
	file, err := os.CreateTemp(filepath.Dir(path), buildTempPattern(path, "tar"))
	if err != nil {
		return nil, err
	}
	o := &tarBatchOutput{path: path, file: file, modTime: time.Now().Truncate(time.Second)}
	var w io.Writer = file
	switch compression {
	case "":
	case "gz":
		o.compressor = gzip.NewWriter(file)
	case "bz2":
		o.compressor, err = dsnetbzip2.NewWriter(file, nil)
	case "xz":
		o.compressor, o.wait, err = startXZ(file, xzThreads)
	case "zst":
		o.compressor, err = zstd.NewWriter(file)
	default:
		err = fmt.Errorf("unsupported tar compression %q", compression)
	}
	if err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, err
	}
	if o.compressor != nil {
		w = o.compressor
	}
	o.tw = tar.NewWriter(w)
	return o, nil
}

// startXZ starts an external xz writing to out, as archive building does,
// since it is much faster than a Go xz encoder and can use several threads.
func startXZ(out io.Writer, threads int) (io.WriteCloser, func() error, error) {
	if threads <= 0 {
		threads = 1
	}
	cmd := exec.Command("xz", fmt.Sprintf("-T%d", threads), "-c")
	cmd.Stdout = out
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start xz: %w", err)
	}
	wait := func() error {
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("xz compression failed: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil
	}
	return stdin, wait, nil
}

func (o *tarBatchOutput) write(name string, data []byte) error {
	header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), ModTime: o.modTime, Typeflag: tar.TypeReg}
	if err := o.tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := o.tw.Write(data)
	return err
}

func (o *tarBatchOutput) close() error {
	err := o.tw.Close()
	if o.compressor != nil {
		if closeErr := o.compressor.Close(); err == nil {
			err = closeErr
		}
	}
	if o.wait != nil {
		if waitErr := o.wait(); err == nil {
			err = waitErr
		}
	}
	if closeErr := o.file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(o.file.Name(), o.path)
	}
	if err != nil {
		os.Remove(o.file.Name())
	}
	return err
}

func (o *tarBatchOutput) abort() {
	if o.compressor != nil {
		o.compressor.Close()
	}
	if o.wait != nil {
		o.wait()
	}
	o.file.Close()
	os.Remove(o.file.Name())
}
