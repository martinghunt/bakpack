# Extract annotations

Extract one or more annotations from a `.bakpack` archive:

```
bakpack extract ARCHIVE SAMPLE... [flags]
```

`ARCHIVE` can be a local `.bakpack` path or an HTTP(S) URL. HTTP(S) servers must
support byte-range requests.

## Output modes

```text
--reduced    write SAMPLE.reduced.bakta.json
--original   write SAMPLE.bakta.json
--genome     write SAMPLE.fa
--gff3       write SAMPLE.gff3
--gff3-annotation-only
             write SAMPLE.gff3 without the terminal ##FASTA section
```

If no output mode is selected, `--reduced` is used.

Original JSON, genome FASTA, and GFF3 extraction require a genome source:

```text
--genomes genomes.tar.xz
--genomes-format auto|dir|list|manifest|tar.xz|agc
```

`--genomes` may also be a single genome FASTA file. In auto mode, sample IDs
are inferred from `.fa`, `.fasta`, or `.fna` names, with optional faqt
compression suffixes: `.gz`, `.bz2`, `.xz`, and `.zst`.
When extracting one explicit `SAMPLE`, that sample ID takes precedence over the
FASTA filename; the same applies when `--samples-file` provides exactly one
sample.

## Genome source matching

With a genome directory, `bakpack` looks for a FASTA whose basename matches
each requested sample ID. These names are equivalent for sample `SAMPLE`:

```text
SAMPLE.fa
SAMPLE.fasta.gz
SAMPLE.fna.bz2
SAMPLE.fa.xz
SAMPLE.fasta.zst
```

The compressed input is decompressed automatically before the original JSON,
FASTA, or GFF3 is reconstructed. Output FASTA files are always written as
uncompressed `SAMPLE.fa`.

A single FASTA file supplies one requested sample. Its basename normally
provides the sample ID, but a lone positional `SAMPLE` or the sole line in
`--samples-file` overrides that inferred ID. This makes the following valid
even though the filename does not match `SAMPLE`.

## Examples

Extract one reconstructed annotation:

```
bakpack extract annotations.bakpack SAMPLE \
  --genomes genomes.tar.xz \
  --original \
  --output-dir out
```

Extract the reconstructed annotation and genome:

```
bakpack extract annotations.bakpack SAMPLE \
  --genomes genomes.tar.xz \
  --original \
  --genome \
  --output-dir out
```

Extract from a compressed single-genome FASTA:

```
bakpack extract annotations.bakpack SAMPLE \
  --genomes downloaded-assembly.fasta.gz \
  --original \
  --output-dir out
```

Extract several samples using a directory of genomes. Compressed and
uncompressed FASTA files may be mixed in the directory:

```
bakpack extract annotations.bakpack SAMPLE1 SAMPLE2 SAMPLE3 \
  --genomes genomes/ \
  --original \
  --genome \
  --output-dir out
```

For example, this directory can contain `SAMPLE1.fa.gz`, `SAMPLE2.fasta`, and
`SAMPLE3.fna.zst`.

Extract the reconstructed annotation and GFF3:

```
bakpack extract annotations.bakpack SAMPLE \
  --genomes genomes.tar.xz \
  --original \
  --gff3-annotation-only \
  --output-dir out
```

Extract several reduced annotations:

```
bakpack extract annotations.bakpack SAMPLE1 SAMPLE2 SAMPLE3 \
  --reduced \
  --output-dir out
```

Use a sample list file:

```
bakpack extract annotations.bakpack \
  --samples-file samples.txt \
  --genomes genomes/ \
  --original \
  --gff3-annotation-only \
  --output-dir out
```

The samples file has one sample ID per line. The whole non-comment line is used
as the sample ID. In this example, each listed sample is matched to the
corresponding genome in `genomes/`, including compressed FASTA files.

Extract only reduced JSON for samples in a file; no genome source is needed:

```
bakpack extract annotations.bakpack \
  --samples-file samples.txt \
  --reduced \
  --output-dir out
```

Extract over HTTP(S):

```
bakpack extract https://example.org/annotations.bakpack SAMPLE \
  --genomes genomes.tar.xz \
  --original \
  --output-dir out
```

`bakpack` reads the front index and only decompresses chunks containing
requested samples.
