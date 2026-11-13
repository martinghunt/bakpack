"""Write GFF3 for a Bakta JSON with Bakta's own writer, as bakta would.

Usage: python3 bakta_gff_ref.py BAKTA_REPO JSON OUT_GFF3 [--compliant] [--annotate-compliant]

--annotate-compliant also applies the changes Bakta --compliant makes while
annotating (dropping contigs under 200 bp and revising CDS/sORF products),
to show what a --compliant run would write for JSON from a run without it.
"""
import json
import sys
from pathlib import Path

bakta_repo, json_path, out_path = sys.argv[1:4]
compliant = '--compliant' in sys.argv[4:]
annotate_compliant = '--annotate-compliant' in sys.argv[4:]
sys.path.insert(0, bakta_repo)

import bakta.config as cfg  # noqa: E402
import bakta.io.gff as gff  # noqa: E402
import bakta.io.insdc as insdc  # noqa: E402

with open(json_path) as fh:
    data = json.load(fh)

# The settings bakta_io takes from the JSON, plus the compliant switch it lacks.
cfg.version = data['version']['bakta']
db_version = data['version']['db']['version'].split('.')
cfg.db_info = {'type': data['version']['db']['type'], 'major': db_version[0], 'minor': db_version[1]}
cfg.translation_table = data['genome']['translation_table']
cfg.compliant = compliant

if annotate_compliant:
    data['sequences'] = [seq for seq in data['sequences'] if seq['length'] >= 200]
    kept = {seq['id'] for seq in data['sequences']}
    data['features'] = [feat for feat in data['features'] if feat['sequence'] in kept]
    for feat in data['features']:
        if feat['type'] in ('cds', 'sorf') and feat.get('product'):
            feat['product'] = insdc.revise_product_insdc(feat['product'])

features_by_sequence = {seq['id']: [] for seq in data['sequences']}
for feature in data['features']:
    seq_id = feature['sequence'] if 'sequence' in feature else feature['contig']
    features_by_sequence[seq_id].append(feature)

gff.write_features(data, features_by_sequence, Path(out_path))
