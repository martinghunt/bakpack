# bakpack Archive Format

This document describes the `.bakpack` archive format written by the current
Go implementation.

The format is designed for two properties:

- compact storage of Bakta annotation JSON after removing fields derivable from
  the matching genome sequence
- efficient extraction of one or more samples from local files or HTTP(S)
  servers that support byte-range requests

The genome FASTA is not stored in the `.bakpack` archive. Reconstructing the
original Bakta JSON, or extracting genome FASTA, requires the matching genome
source to be supplied separately.

## Compatibility

Current archives use:

```text
archive magic:  BAKPACK1
archive version: 1
payload format: specialized_columnar_chunklocal_v9
```

Readers must reject archives whose `format`, `version`, or `payload_format` they
do not support.

The format should be treated as pre-1.0 until there is a tagged release that
explicitly says otherwise. Compatible index additions may be made by adding JSON
fields. Changes to the fixed file layout or checksum semantics should bump the
archive version. Changes to chunk payload encoding should use a new
`payload_format`.

## File Layout

All multi-byte fixed-width integers in the archive header are little-endian.
All variable-length integers inside chunk payloads use Go's
`encoding/binary.PutUvarint` encoding.

```text
offset  size        contents
0       8           ASCII magic: "BAKPACK1"
8       8           uint64 little-endian compressed index length, N
16      N           xz-compressed archive index JSON
16+N    remaining   xz-compressed chunk payloads, concatenated
```

There is no footer. The front index makes HTTP range extraction predictable:

1. Read bytes `0..15` to get the magic and index length.
2. Read bytes `16..15+N` to get the compressed index.
3. Read only the compressed chunk ranges needed for requested samples.

For one sample in one chunk, this is normally three byte-range requests: fixed
header, index, and one chunk.

## Compression

The index and every chunk payload are independent xz streams.

The writer uses the external `xz` program:

```text
xz -9e -T1 -c
```

The thread count is configurable.

Readers only require xz decompression support.

## Archive Index

After xz decompression, the index is JSON with this logical shape:

```json
{
  "format": "bakpack",
  "version": 1,
  "payload_format": "specialized_columnar_chunklocal_v9",
  "chunk_size": 25,
  "chunks": [
    {
      "id": 0,
      "offset": 0,
      "compressed_size": 1234,
      "uncompressed_size": 5678,
      "top_keys": ["..."],
      "value_schemas": [
        {"schema_id": 0, "keys": ["..."]}
      ],
      "feature_schemas": [
        {"schema_id": 0, "keys": ["..."]}
      ],
      "feature_fields": ["..."],
      "field_codecs": [
        {"field": "contig", "kind": "sequence_index"}
      ]
    }
  ],
  "samples": [
    {
      "sample_id": "sampleA",
      "annotation_name": "sampleA.bakta.json",
      "genome_name": "sampleA.fa",
      "chunk_id": 0,
      "original_json_canonical_sha256": "...",
      "reduced_json_canonical_sha256": "..."
    }
  ]
}
```

`chunks[].offset` is relative to the start of the chunk area, not relative to
the beginning of the file. The absolute byte offset of a compressed chunk is:

```text
16 + compressed_index_length + chunks[i].offset
```

`samples[].chunk_id` identifies the chunk containing that sample.

The sample order in the index is the build order. Samples are assigned to chunks
in contiguous groups of `chunk_size`.

Each chunk entry also stores the codec metadata required to decode that chunk.
Codecs are chunk-local: schemas and field dictionaries are learned from only the
samples in that chunk. This lets archive creation reduce and encode one chunk at
a time without first scanning the whole archive.

## Checksums

Every sample stores two SHA-256 checksums:

```text
original_json_canonical_sha256
reduced_json_canonical_sha256
```

The checksums are SHA-256 over bakpack canonical JSON:

- exactly one JSON value is parsed
- object keys are sorted lexicographically
- no insignificant whitespace is emitted
- strings use normal JSON string escaping
- integer JSON numbers keep their decimal text
- non-integer numbers are normalized with Go `strconv.FormatFloat` using
  format `'g'`, precision `-1`, and bit size `64`

Extraction always verifies reduced JSON against `reduced_json_canonical_sha256`.

Reduced JSON also contains the original canonical checksum in a top-level
`_bakpack` metadata object. Restoring original JSON removes this metadata before
reconstruction and verifies the reconstructed canonical SHA-256 against the
embedded value. Archive extraction also verifies reconstructed original JSON
against the archive index `original_json_canonical_sha256`.

Exact original JSON bytes are not preserved. Correctness is defined by canonical
JSON content.

## Reduced Bakta JSON

The archive stores reduced Bakta JSON, not the original JSON bytes. A standalone
reduced JSON file has this additional top-level object:

```json
{
  "_bakpack": {
    "format": "bakpack_reduced_bakta_json",
    "version": 1,
    "original_json_canonical_sha256": "..."
  }
}
```

The `_bakpack` object is bakpack metadata, not part of the original Bakta JSON.
It is removed during restore.

During reduction, bakpack removes a value only when it can independently derive
the same value from the matching genome FASTA and the remaining annotation
content. The current reducer can remove:

- `sequences[].sequence`
- feature `nt`
- feature `aa`
- `stats.no_sequences`
- `stats.size`
- `stats.n_ratio`
- `stats.n50`
- `sequences[].length`
- protein feature `aa_hexdigest`
- CDS feature `start_type`
- `hypothetical` when `product` is `hypothetical protein`
- gap feature `length`

Reduced JSON files emitted by the library are pretty-printed with two-space
indentation and a trailing newline.

Restoring original JSON re-derives missing values from the genome FASTA and then
pretty-prints JSON with the same writer. The restored output is considered
correct when its canonical SHA-256 matches the embedded original canonical
checksum.

## Optimized Chunk Payload

Each compressed chunk decompresses to a `specialized_columnar_chunklocal_v9`
payload.

Strings are encoded as:

```text
uvarint byte_length
byte[byte_length] UTF-8 data
```

Signed integers are encoded with zigzag transformation followed by uvarint:

```text
uint64(value << 1) ^ uint64(value >> 63)
```

The uncompressed chunk layout is:

```text
ASCII "BSC8"
uvarint n_samples
uvarint n_fields
uvarint metadata_stream_length
uvarint schema_stream_length
repeat n_fields:
  uvarint field_stream_length
repeat n_samples:
  string sample_id
  string annotation_name
  uvarint feature_count
  uvarint metadata_offset
  uvarint metadata_length
  uvarint schema_offset
  uvarint schema_length
  repeat n_fields:
    uvarint field_offset
    uvarint field_length
byte[metadata_stream_length] metadata stream
byte[schema_stream_length] feature schema stream
repeat n_fields:
  byte[field_stream_length] field stream
```

`n_fields` must equal that chunk index entry's `feature_fields` length.

The metadata stream contains each sample's top-level JSON object excluding the
`features` array. Metadata values use the generic JSON value encoding described
below.

The feature schema stream contains one schema ID per feature, in feature order.
Each schema ID indexes `chunks[].feature_schemas` for that chunk. A feature
schema is the sorted list of keys present in that feature object.

Each field stream contains values for one feature field, in that chunk's
`feature_fields` order. For a given sample, the directory gives the slice of
every stream that belongs to that sample.

To decode one sample:

1. Decode that sample's metadata slice.
2. Decode its feature schema IDs.
3. Count how many values are needed for each feature field.
4. Decode that sample's slice from each field stream.
5. Reassemble features by walking that chunk's feature schemas in feature order.
6. Reassemble the top-level object in that chunk's `top_keys` order, inserting
   the restored `features` array at the `features` key.

## Chunk Codec Metadata

Each chunk index entry stores the codec metadata needed to decode that optimized
chunk:

```text
top_keys         sorted top-level keys for reduced JSON objects
value_schemas   object schemas used by generic JSON value encoding
feature_schemas feature-object key sets used by the schema stream
feature_fields  sorted union of feature keys across the chunk
field_codecs    one codec descriptor per feature field
```

All samples in a chunk must have the same top-level reduced JSON key set.

## Field Codecs

`field_codecs[i]` describes how values in `feature_fields[i]` are encoded.

Supported field codec kinds:

```text
sequence_index
sample_prefix_uint_string
const_null
const_bool
const_string
bool_bitset
uint
int
float64
raw_number
raw_string
nullable_raw_string
enum_string
nullable_enum_string
generic
```

Codec meanings:

- `sequence_index`: value is a uvarint index into metadata `sequences[].id`.
- `sample_prefix_uint_string`: per-sample stream stores a common string prefix,
  fixed numeric width, then uvarint numeric suffixes.
- `const_null`, `const_bool`, `const_string`: no per-value payload bytes; the
  value is implied by the codec descriptor.
- `bool_bitset`: booleans packed least-significant-bit first, eight values per
  byte.
- `uint`: non-negative integers as uvarint.
- `int`: signed integers as zigzag uvarint.
- `float64`: IEEE-754 little-endian float64.
- `raw_number`: JSON number text as a string.
- `raw_string`: string values.
- `nullable_raw_string`: uvarint `0` for null, otherwise `length+1` followed by
  string bytes.
- `enum_string`: uvarint index into codec `values`.
- `nullable_enum_string`: uvarint `0` for null, otherwise one-based index into
  codec `values`.
- `generic`: generic JSON value encoding.

## Generic JSON Value Encoding

The generic value encoder is used for metadata, nested JSON values, and feature
fields that do not fit a more specific codec.

Each value starts with a one-byte tag:

```text
0 null
1 false
2 true
3 int      zigzag uvarint
4 float64  IEEE-754 little-endian float64
5 string   string encoding
6 list     uvarint length, followed by values
7 object   uvarint value_schema_id, followed by values in schema key order
8 number   JSON number text as string
```

Object values refer to that chunk's `value_schemas`. The schema gives the sorted
key order, so object keys are not repeated in the chunk payload.
