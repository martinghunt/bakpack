# Checksums

Each archive sample stores:

```text
original_json_canonical_sha256
reduced_json_canonical_sha256
```

Canonical hashes ignore JSON object key order and whitespace but preserve array
order and values.

Reduced JSON files include a top-level `_bakpack` metadata object containing
`original_json_canonical_sha256`. `restore` removes this metadata before
reconstructing the original JSON and verifies the reconstructed canonical
SHA-256 automatically.

Archive extraction always verifies reduced JSON canonical SHA-256.
Reconstructed original JSON extraction verifies original canonical SHA-256.

## Canonical JSON

The canonical JSON used for checksums has these properties:

- exactly one JSON value is parsed
- object keys are sorted lexicographically
- no insignificant whitespace is emitted
- strings use normal JSON string escaping
- integer JSON numbers keep their decimal text
- non-integer numbers are normalized by Go formatting

Exact original JSON bytes are not preserved. Correctness is defined by canonical
JSON content.
