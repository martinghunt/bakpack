# Archive format

The detailed archive format is described in
[FORMAT.md](https://github.com/martinghunt/bakpack/blob/main/FORMAT.md).

Current archives use:

```text
archive magic:  BAKPACK1
archive version: 1
payload format: specialized_columnar_chunklocal_v9
```

The genome FASTA is not stored in the `.bakpack` archive. Reconstructing the
original Bakta JSON, or extracting genome FASTA, requires the matching genome
source to be supplied separately.

## File layout

```text
offset  size        contents
0       8           ASCII magic: "BAKPACK1"
8       8           uint64 little-endian compressed index length, N
16      N           xz-compressed archive index JSON
16+N    remaining   xz-compressed chunk payloads, concatenated
```

There is no footer. The front index makes HTTP range extraction predictable:

1. Read bytes `0..15` to get the magic and index length.
2. Read `N` bytes starting at offset `16` to get the compressed index.
3. Read only the compressed chunk ranges needed for requested samples.

For one sample in one chunk, this is normally three byte-range requests.

## Compression

The index and every chunk payload are independent xz streams. The writer uses
the external `xz` program:

```text
xz -9e -T1 -c
```

The thread count is configurable. Readers only require xz decompression support.

## Chunk payload

Each compressed chunk decompresses to a specialized columnar payload. Within
each chunk, Bakta feature values are stored as typed streams instead of repeated
JSON objects. High-volume fields get specialized codecs, including contig
indexes and sample-local numeric suffix encoding for `id` and `locus`.

Schemas and field codecs are chunk-local, so archive creation can reduce and
encode one chunk of samples at a time.

## Compatibility

The format should be treated as pre-1.0 until there is a tagged release that
explicitly says otherwise. Readers must reject archives whose `format`,
`version`, or `payload_format` they do not support.
