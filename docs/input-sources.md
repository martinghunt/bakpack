# Input sources

Annotation sources support:

- directory
- file list
- combined manifest
- `.tar.xz`

Genome sources support:

- directory
- file list
- combined manifest
- `.tar.xz`
- `.agc`

## Sample IDs

Sample IDs are inferred from common names:

```text
sample.bakta.json -> sample
sample.json       -> sample
sample.fa         -> sample
sample.fasta      -> sample
sample.fna        -> sample
```

For `.tar.xz` sources, `bakpack` uses regular tar member basenames in tar order.
For directories, it uses lexicographic file path order. For AGC genome sources,
sample names come from `agc listset`.

## File lists

File lists can contain one path per line:

```text
path/to/sampleA.bakta.json
path/to/sampleB.bakta.json
```

Or explicit sample/path pairs:

```text
sampleA path/to/sampleA.bakta.json
sampleB path/to/sampleB.bakta.json
```

Relative paths are resolved relative to the list file.

## Combined manifest

A combined manifest can be used with `bakpack build --manifest` or as a source
with `--annotations-format manifest` / `--genomes-format manifest`.

It has three whitespace-separated columns:

```text
sample_id  annotation_json       genome_fasta
sampleA    path/to/a.bakta.json  path/to/a.fa
sampleB    path/to/b.bakta.json  path/to/b.fa
```

The header row is optional. Blank lines and lines starting with `#` are ignored.
Relative paths are resolved relative to the manifest file. The row order is used
as the source order.

The same manifest can be used for extraction when original JSON or FASTA output
needs the matching genome:

```
bakpack extract annotations.bakpack sampleA \
  --genomes samples.tsv \
  --genomes-format manifest \
  --original
```
