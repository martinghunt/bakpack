package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/martinghunt/bakpack"
	"github.com/martinghunt/bakpack/internal/buildinfo"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "bakpack",
		Short:   "Compress and retrieve Bakta annotation JSON files",
		Version: displayVersion(buildinfo.Version),
	}
	cmd.SetVersionTemplate("{{.Name}} {{.Version}}\n")
	cmd.AddCommand(newReduceCommand())
	cmd.AddCommand(newRestoreCommand())
	cmd.AddCommand(newGFF3Command())
	cmd.AddCommand(newBuildCommand())
	cmd.AddCommand(newExtractCommand())
	cmd.AddCommand(newIndexCommand())
	return cmd
}

func displayVersion(raw string) string {
	if len(raw) > 1 && (raw[0] == 'v' || raw[0] == 'V') && raw[1] >= '0' && raw[1] <= '9' {
		return raw[1:]
	}
	return raw
}

func newReduceCommand() *cobra.Command {
	var output string
	var batch batchFlags
	cmd := &cobra.Command{
		Use:   "reduce [BAKTA_JSON GENOME_FASTA]",
		Short: "Write reduced Bakta JSON by removing genome-derived fields",
		Long: `Write reduced Bakta JSON by removing genome-derived fields.

With BAKTA_JSON and GENOME_FASTA, reduce one sample to the --output file.
Without them, reduce every sample in --annotations and --genomes (or
--manifest), writing SAMPLE.reduced.bakta.json files to --output, which is a
directory or a tar file (.tar, .tar.gz, .tar.bz2, .tar.xz, .tar.zst).`,
		Args: singleOrBatchArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireOutput(output); err != nil {
				return err
			}
			if len(args) == 0 {
				opts, err := batch.options(output, cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				return bakpack.ReduceBatch(cmd.Context(), opts)
			}
			if err := batch.requireUnset(); err != nil {
				return err
			}
			annotation, genome, err := readJSONAndGenome(args[0], args[1])
			if err != nil {
				return err
			}
			result, err := bakpack.ReduceBaktaJSON(annotation, genome)
			if err != nil {
				return err
			}
			if err := os.WriteFile(output, result.ReducedJSON, 0o644); err != nil {
				return err
			}
			if len(result.UnmatchedSequences) != 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), bakpack.UnmatchedSequencesWarning(args[0], result.UnmatchedSequences))
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "original_json_canonical_sha256\t%s\n", result.Original.CanonicalSHA256)
			fmt.Fprintf(cmd.ErrOrStderr(), "reduced_json_canonical_sha256\t%s\n", result.Reduced.CanonicalSHA256)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output reduced JSON file, or in batch mode a directory or tar file")
	batch.add(cmd)
	return cmd
}

func newRestoreCommand() *cobra.Command {
	var output string
	var batch batchFlags
	cmd := &cobra.Command{
		Use:   "restore [REDUCED_JSON GENOME_FASTA]",
		Short: "Recreate original Bakta JSON content from reduced JSON and genome FASTA",
		Long: `Recreate original Bakta JSON content from reduced JSON and genome FASTA.

With REDUCED_JSON and GENOME_FASTA, restore one sample to the --output file.
Without them, restore every sample in --annotations and --genomes (or
--manifest), writing SAMPLE.bakta.json files to --output, which is a directory
or a tar file (.tar, .tar.gz, .tar.bz2, .tar.xz, .tar.zst). Reduced JSON
named SAMPLE.reduced.bakta.json pairs with genome SAMPLE.fa.`,
		Args: singleOrBatchArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireOutput(output); err != nil {
				return err
			}
			if len(args) == 0 {
				opts, err := batch.options(output, cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				return bakpack.RestoreBatch(cmd.Context(), opts)
			}
			if err := batch.requireUnset(); err != nil {
				return err
			}
			reduced, genome, err := readJSONAndGenome(args[0], args[1])
			if err != nil {
				return err
			}
			result, err := bakpack.RestoreBaktaJSON(reduced, genome)
			if err != nil {
				return err
			}
			if err := os.WriteFile(output, result.OriginalJSON, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "original_json_canonical_sha256\t%s\n", result.Original.CanonicalSHA256)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output reconstructed JSON file, or in batch mode a directory or tar file")
	batch.add(cmd)
	return cmd
}

func newGFF3Command() *cobra.Command {
	var output string
	var annotationOnly bool
	var batch batchFlags
	cmd := &cobra.Command{
		Use:   "gff3 [BAKTA_JSON GENOME_FASTA]",
		Short: "Render a Bakta JSON annotation as GFF3",
		Long: `Render a Bakta JSON annotation, original or reduced, as GFF3.

With BAKTA_JSON and GENOME_FASTA, render one sample to the --output file.
Without them, render every sample in --annotations and --genomes (or
--manifest), writing SAMPLE.gff3 files to --output, which is a directory or a
tar file (.tar, .tar.gz, .tar.bz2, .tar.xz, .tar.zst).`,
		Args: singleOrBatchArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireOutput(output); err != nil {
				return err
			}
			if len(args) == 0 {
				opts, err := batch.options(output, cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				opts.GFF3AnnotationOnly = annotationOnly
				return bakpack.GFF3Batch(cmd.Context(), opts)
			}
			if err := batch.requireUnset(); err != nil {
				return err
			}
			annotation, genome, err := readJSONAndGenome(args[0], args[1])
			if err != nil {
				return err
			}
			gff3, err := bakpack.BaktaGFF3WithOptions(annotation, genome, bakpack.BaktaGFF3Options{
				AnnotationOnly: annotationOnly,
			})
			if err != nil {
				return err
			}
			return os.WriteFile(output, gff3, 0o644)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output GFF3 file, or in batch mode a directory or tar file")
	cmd.Flags().BoolVar(&annotationOnly, "annotation-only", false, "Omit the terminal FASTA section")
	batch.add(cmd)
	return cmd
}

// batchFlags are the source flags shared by reduce, restore and gff3 batch
// mode.
type batchFlags struct {
	annotationsPath, annotationsFormat string
	genomesPath, genomesFormat         string
	manifestPath                       string
	orderPath                          string
	spoolCompression                   string
	xzThreads                          int
}

func (f *batchFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.annotationsPath, "annotations", "", "Batch mode annotation source: directory, file list, or tar (.tar, .tar.gz, .tar.bz2, .tar.xz, .tar.zst)")
	cmd.Flags().StringVar(&f.annotationsFormat, "annotations-format", "auto", "Annotation source format: auto, dir, list, manifest, tar, tar.gz, tar.bz2, tar.xz, tar.zst")
	cmd.Flags().StringVar(&f.genomesPath, "genomes", "", "Batch mode genome source: directory, file list, tar (.tar, .tar.gz, .tar.bz2, .tar.xz, .tar.zst), or .agc")
	cmd.Flags().StringVar(&f.genomesFormat, "genomes-format", "auto", "Genome source format: auto, dir, list, manifest, tar, tar.gz, tar.bz2, tar.xz, tar.zst, agc")
	cmd.Flags().StringVar(&f.manifestPath, "manifest", "", "Batch mode tab-delimited manifest with columns: sample_id, annotation_json, genome_fasta")
	cmd.Flags().StringVar(&f.orderPath, "order", "", "Batch mode file of sample IDs defining output order")
	cmd.Flags().StringVar(&f.spoolCompression, "annotation-spool-compression", "gzip", "Compression of temporary files holding tar members until they are paired: gzip or none")
	cmd.Flags().IntVar(&f.xzThreads, "xz-threads", 1, "Threads passed to xz as -T when writing a .tar.xz")
}

func (f *batchFlags) options(output string, warnings io.Writer) (bakpack.BatchOptions, error) {
	annotations, genomes, err := openBuildSources(f.manifestPath, f.annotationsPath, f.annotationsFormat, f.genomesPath, f.genomesFormat)
	if err != nil {
		return bakpack.BatchOptions{}, err
	}
	order, err := readNameFile(f.orderPath)
	if err != nil {
		return bakpack.BatchOptions{}, err
	}
	return bakpack.BatchOptions{
		Annotations:      annotations,
		Genomes:          genomes,
		Order:            order,
		OutputPath:       output,
		XZThreads:        f.xzThreads,
		SpoolCompression: f.spoolCompression,
		Warnings:         warnings,
	}, nil
}

// requireUnset rejects batch source flags in single-sample mode.
func (f *batchFlags) requireUnset() error {
	if f.annotationsPath != "" || f.genomesPath != "" || f.manifestPath != "" || f.orderPath != "" {
		return fmt.Errorf("--annotations, --genomes, --manifest and --order are only used without positional arguments")
	}
	return nil
}

func singleOrBatchArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 0 && len(args) != 2 {
		return fmt.Errorf("expected two arguments for one sample, or none for batch mode, got %d", len(args))
	}
	return nil
}

func requireOutput(path string) error {
	if path == "" {
		return fmt.Errorf("--output is required")
	}
	return nil
}

func readJSONAndGenome(jsonPath, genomePath string) ([]byte, bakpack.Genome, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, bakpack.Genome{}, err
	}
	genomeBytes, err := os.ReadFile(genomePath)
	if err != nil {
		return nil, bakpack.Genome{}, err
	}
	sampleID := sampleIDFromPath(jsonPath)
	genome, err := bakpack.ReadGenome(sampleID, filepath.Base(genomePath), genomeBytes)
	if err != nil {
		return nil, bakpack.Genome{}, err
	}
	return data, genome, nil
}

func newBuildCommand() *cobra.Command {
	var annotationsPath, annotationsFormat string
	var genomesPath, genomesFormat string
	var manifestPath string
	var output string
	var orderPath string
	var annotationSpoolCompression string
	var chunkSize int
	var xzThreads int
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a .bakpack archive from Bakta JSON and genome sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			if output == "" {
				output = "annotations.bakpack"
			}
			annotations, genomes, err := openBuildSources(manifestPath, annotationsPath, annotationsFormat, genomesPath, genomesFormat)
			if err != nil {
				return err
			}
			order, err := readNameFile(orderPath)
			if err != nil {
				return err
			}
			return bakpack.BuildArchive(cmd.Context(), bakpack.BuildOptions{
				Annotations:                annotations,
				Genomes:                    genomes,
				Order:                      order,
				ChunkSize:                  chunkSize,
				OutputPath:                 output,
				XZThreads:                  xzThreads,
				AnnotationSpoolCompression: annotationSpoolCompression,
				Warnings:                   cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&annotationsPath, "annotations", "", "Annotation source: directory, file list, or tar (.tar, .tar.gz, .tar.bz2, .tar.xz, .tar.zst)")
	cmd.Flags().StringVar(&annotationsFormat, "annotations-format", "auto", "Annotation source format: auto, dir, list, manifest, tar, tar.gz, tar.bz2, tar.xz, tar.zst")
	cmd.Flags().StringVar(&genomesPath, "genomes", "", "Genome source: directory, file list, tar (.tar, .tar.gz, .tar.bz2, .tar.xz, .tar.zst), or .agc")
	cmd.Flags().StringVar(&genomesFormat, "genomes-format", "auto", "Genome source format: auto, dir, list, manifest, tar, tar.gz, tar.bz2, tar.xz, tar.zst, agc")
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "Tab-delimited manifest with columns: sample_id, annotation_json, genome_fasta")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output archive path, default annotations.bakpack")
	cmd.Flags().StringVar(&orderPath, "order", "", "Optional file of sample IDs defining archive order")
	cmd.Flags().StringVar(&annotationSpoolCompression, "annotation-spool-compression", "gzip", "Compression of temporary files holding tar members until they are paired: gzip or none")
	cmd.Flags().IntVar(&chunkSize, "chunk-size", 25, "Samples per compressed chunk")
	cmd.Flags().IntVar(&xzThreads, "xz-threads", 1, "Threads passed to xz as -T")
	return cmd
}

func openBuildSources(manifestPath, annotationsPath, annotationsFormat, genomesPath, genomesFormat string) (bakpack.FileSource, bakpack.FileSource, error) {
	if manifestPath != "" {
		if annotationsPath != "" || genomesPath != "" {
			return nil, nil, fmt.Errorf("--manifest cannot be used with --annotations or --genomes")
		}
		return bakpack.OpenManifestSources(manifestPath)
	}
	if annotationsPath == "" {
		return nil, nil, fmt.Errorf("--annotations is required")
	}
	if genomesPath == "" {
		return nil, nil, fmt.Errorf("--genomes is required")
	}
	annotations, err := bakpack.OpenSource(annotationsPath, annotationsFormat, "annotation")
	if err != nil {
		return nil, nil, err
	}
	genomes, err := bakpack.OpenSource(genomesPath, genomesFormat, "genome")
	if err != nil {
		return nil, nil, err
	}
	return annotations, genomes, nil
}

func newExtractCommand() *cobra.Command {
	var genomesPath, genomesFormat string
	var outputDir string
	var samplesFile string
	var reduced, original, genome, gff3, gff3AnnotationOnly bool
	cmd := &cobra.Command{
		Use:   "extract ARCHIVE SAMPLE...",
		Short: "Extract one or more annotations from a .bakpack archive",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			archive := args[0]
			samples := append([]string(nil), args[1:]...)
			fromFile, err := readNameFile(samplesFile)
			if err != nil {
				return err
			}
			samples = append(samples, fromFile...)
			genomes, err := openOptionalGenomeSource(genomesPath, genomesFormat)
			if err != nil {
				return err
			}
			if len(samples) == 1 {
				if source, ok := genomes.(bakpack.SingleFileSource); ok {
					genomes = source.WithSampleID(samples[0])
				}
			}
			return bakpack.ExtractArchive(cmd.Context(), bakpack.ExtractOptions{
				ArchivePath:        archive,
				Genomes:            genomes,
				Samples:            samples,
				OutputDir:          outputDir,
				Reduced:            reduced,
				Original:           original,
				Genome:             genome,
				GFF3:               gff3,
				GFF3AnnotationOnly: gff3AnnotationOnly,
			})
		},
	}
	cmd.Flags().StringVar(&genomesPath, "genomes", "", "Genome source for original JSON/FASTA/GFF3 extraction")
	cmd.Flags().StringVar(&genomesFormat, "genomes-format", "auto", "Genome source format: auto, dir, list, manifest, tar, tar.gz, tar.bz2, tar.xz, tar.zst, agc")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", ".", "Output directory")
	cmd.Flags().StringVar(&samplesFile, "samples-file", "", "File of sample IDs to extract")
	cmd.Flags().BoolVar(&reduced, "reduced", false, "Write reduced JSON")
	cmd.Flags().BoolVar(&original, "original", false, "Write reconstructed original JSON")
	cmd.Flags().BoolVar(&genome, "genome", false, "Write matching genome FASTA")
	cmd.Flags().BoolVar(&gff3, "gff3", false, "Write rendered GFF3 annotation")
	cmd.Flags().BoolVar(&gff3AnnotationOnly, "gff3-annotation-only", false, "Write GFF3 without the terminal FASTA section")
	return cmd
}

func openOptionalGenomeSource(path, format string) (bakpack.FileSource, error) {
	if path == "" {
		return nil, nil
	}
	return bakpack.OpenSource(path, format, "genome")
}

func newIndexCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index ARCHIVE",
		Short: "Print a bakpack archive's internal index as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			index, err := bakpack.ReadArchiveIndex(args[0])
			if err != nil {
				return err
			}
			data, err := bakpack.PrettyJSON(index)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
	return cmd
}

func readNameFile(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		names = append(names, line)
	}
	return names, nil
}

func sampleIDFromPath(path string) string {
	base := filepath.Base(path)
	for _, suffix := range bakpack.AnnotationJSONSuffixes {
		if strings.HasSuffix(base, suffix) {
			return strings.TrimSuffix(base, suffix)
		}
	}
	return base
}

func init() {
	cobra.EnableCommandSorting = false
}
