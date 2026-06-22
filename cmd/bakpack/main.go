package main

import (
	"fmt"
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
		Version: buildinfo.Version,
	}
	cmd.AddCommand(newReduceCommand())
	cmd.AddCommand(newRestoreCommand())
	cmd.AddCommand(newBuildCommand())
	cmd.AddCommand(newExtractCommand())
	cmd.AddCommand(newIndexCommand())
	return cmd
}

func newReduceCommand() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "reduce BAKTA_JSON GENOME_FASTA",
		Short: "Write reduced Bakta JSON by removing genome-derived fields",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if output == "" {
				return fmt.Errorf("--output is required")
			}
			annotation, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			genomeBytes, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			sampleID := sampleIDFromPath(args[0], "annotation")
			genome, err := bakpack.ReadGenome(sampleID, filepath.Base(args[1]), genomeBytes)
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
			fmt.Fprintf(cmd.ErrOrStderr(), "original_json_canonical_sha256\t%s\n", result.Original.CanonicalSHA256)
			fmt.Fprintf(cmd.ErrOrStderr(), "reduced_json_canonical_sha256\t%s\n", result.Reduced.CanonicalSHA256)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output reduced JSON file")
	return cmd
}

func newRestoreCommand() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "restore REDUCED_JSON GENOME_FASTA",
		Short: "Recreate original Bakta JSON content from reduced JSON and genome FASTA",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if output == "" {
				return fmt.Errorf("--output is required")
			}
			reduced, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			genomeBytes, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			sampleID := sampleIDFromPath(args[0], "annotation")
			genome, err := bakpack.ReadGenome(sampleID, filepath.Base(args[1]), genomeBytes)
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
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output reconstructed JSON file")
	return cmd
}

func newBuildCommand() *cobra.Command {
	var annotationsPath, annotationsFormat string
	var genomesPath, genomesFormat string
	var output string
	var orderPath string
	var chunkSize int
	var xzThreads int
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a .bakpack archive from Bakta JSON and genome sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			if annotationsPath == "" {
				return fmt.Errorf("--annotations is required")
			}
			if genomesPath == "" {
				return fmt.Errorf("--genomes is required")
			}
			if output == "" {
				output = "annotations.bakpack"
			}
			annotations, err := bakpack.OpenSource(annotationsPath, annotationsFormat, "annotation")
			if err != nil {
				return err
			}
			genomes, err := bakpack.OpenSource(genomesPath, genomesFormat, "genome")
			if err != nil {
				return err
			}
			order, err := readNameFile(orderPath)
			if err != nil {
				return err
			}
			return bakpack.BuildArchive(cmd.Context(), bakpack.BuildOptions{
				Annotations: annotations,
				Genomes:     genomes,
				Order:       order,
				ChunkSize:   chunkSize,
				OutputPath:  output,
				XZThreads:   xzThreads,
			})
		},
	}
	cmd.Flags().StringVar(&annotationsPath, "annotations", "", "Annotation source: directory, file list, or .tar.xz")
	cmd.Flags().StringVar(&annotationsFormat, "annotations-format", "auto", "Annotation source format: auto, dir, list, tar.xz")
	cmd.Flags().StringVar(&genomesPath, "genomes", "", "Genome source: directory, file list, .tar.xz, or .agc")
	cmd.Flags().StringVar(&genomesFormat, "genomes-format", "auto", "Genome source format: auto, dir, list, tar.xz, agc")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output archive path, default annotations.bakpack")
	cmd.Flags().StringVar(&orderPath, "order", "", "Optional file of sample IDs defining archive order")
	cmd.Flags().IntVar(&chunkSize, "chunk-size", 25, "Samples per compressed chunk")
	cmd.Flags().IntVar(&xzThreads, "xz-threads", 1, "Threads passed to xz as -T")
	return cmd
}

func newExtractCommand() *cobra.Command {
	var genomesPath, genomesFormat string
	var outputDir string
	var samplesFile string
	var reduced, original, genome bool
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
			var genomes bakpack.FileSource
			if genomesPath != "" {
				genomes, err = bakpack.OpenSource(genomesPath, genomesFormat, "genome")
				if err != nil {
					return err
				}
			}
			return bakpack.ExtractArchive(cmd.Context(), bakpack.ExtractOptions{
				ArchivePath: archive,
				Genomes:     genomes,
				Samples:     samples,
				OutputDir:   outputDir,
				Reduced:     reduced,
				Original:    original,
				Genome:      genome,
			})
		},
	}
	cmd.Flags().StringVar(&genomesPath, "genomes", "", "Genome source for original JSON/FASTA extraction")
	cmd.Flags().StringVar(&genomesFormat, "genomes-format", "auto", "Genome source format: auto, dir, list, tar.xz, agc")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", ".", "Output directory")
	cmd.Flags().StringVar(&samplesFile, "samples-file", "", "File of sample IDs to extract")
	cmd.Flags().BoolVar(&reduced, "reduced", false, "Write reduced JSON")
	cmd.Flags().BoolVar(&original, "original", false, "Write reconstructed original JSON")
	cmd.Flags().BoolVar(&genome, "genome", false, "Write matching genome FASTA")
	return cmd
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
		fields := strings.Fields(line)
		names = append(names, fields[0])
	}
	return names, nil
}

func sampleIDFromPath(path, role string) string {
	base := filepath.Base(path)
	switch role {
	case "annotation":
		base = strings.TrimSuffix(base, ".bakta.json")
		base = strings.TrimSuffix(base, ".json")
	case "genome":
		base = strings.TrimSuffix(base, ".fasta")
		base = strings.TrimSuffix(base, ".fa")
		base = strings.TrimSuffix(base, ".fna")
	}
	return base
}

func init() {
	cobra.EnableCommandSorting = false
}
