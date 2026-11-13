"""Write GenBank and EMBL for a Bakta JSON with Bakta's own writers, as bakta_io would.

Usage: python3 bakta_insdc_ref.py BAKTA_REPO JSON OUT_PREFIX [--compliant] [--annotate-compliant]

Writes OUT_PREFIX.gbff and OUT_PREFIX.embl. --annotate-compliant also applies
the changes Bakta --compliant makes while annotating, as bakta_gff_ref.py does.
"""
import json
import sys
from datetime import datetime
from pathlib import Path

bakta_repo, json_path, out_prefix = sys.argv[1:4]
compliant = '--compliant' in sys.argv[4:]
annotate_compliant = '--annotate-compliant' in sys.argv[4:]
sys.path.insert(0, bakta_repo)

import bakta.config as cfg  # noqa: E402
import bakta.io.insdc as insdc  # noqa: E402

with open(json_path) as fh:
    data = json.load(fh)

# The settings bakta_io takes from the JSON, plus the compliant switch it lacks.
cfg.version = data['version']['bakta']
db_version = data['version']['db']['version'].split('.')
cfg.db_info = {'type': data['version']['db']['type'], 'major': db_version[0], 'minor': db_version[1]}
cfg.translation_table = data['genome']['translation_table']
cfg.run_start = datetime.strptime(data['run']['start'], '%Y-%m-%d %H:%M:%S')
cfg.run_end = datetime.strptime(data['run']['end'], '%Y-%m-%d %H:%M:%S')
cfg.compliant = compliant

if annotate_compliant:
    data['sequences'] = [seq for seq in data['sequences'] if seq['length'] >= 200]
    kept = {seq['id'] for seq in data['sequences']}
    data['features'] = [feat for feat in data['features'] if feat.get('sequence', feat.get('contig')) in kept]
    for feat in data['features']:
        if feat['type'] in ('cds', 'sorf') and feat.get('product'):
            feat['product'] = insdc.revise_product_insdc(feat['product'])

insdc.write_features(data, data['features'], Path(out_prefix + '.gbff'), Path(out_prefix + '.embl'))
