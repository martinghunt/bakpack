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
```

If no output mode is selected, `--reduced` is used.

Original JSON and genome FASTA extraction require a genome source:

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

Extract over HTTP(S):

```
bakpack extract https://example.org/annotations.bakpack SAMPLE \
  --genomes genomes.tar.xz \
  --original \
  --output-dir out
```

`bakpack` reads the front index and only decompresses chunks containing
requested samples.
