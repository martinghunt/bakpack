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
```

If no output mode is selected, `--reduced` is used.

Use `--gff3-annotation-only` with `--gff3` to omit the terminal `##FASTA`
section from the GFF3 output.

Original JSON, genome FASTA, and GFF3 extraction require a genome source:

```text
--genomes genomes.tar.xz
--genomes-format auto|dir|list|manifest|tar.xz|agc
```

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

Extract the reconstructed annotation and GFF3:

```
bakpack extract annotations.bakpack SAMPLE \
  --genomes genomes.tar.xz \
  --original \
  --gff3 \
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
  --reduced \
  --output-dir out
```

The samples file has one sample ID per line. The whole non-comment line is used
as the sample ID.

Extract over HTTP(S):

```
bakpack extract https://example.org/annotations.bakpack SAMPLE \
  --genomes genomes.tar.xz \
  --original \
  --output-dir out
```

`bakpack` reads the front index and only decompresses chunks containing
requested samples.
