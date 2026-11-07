import json
import tarfile
from pathlib import Path

root = Path('.work/dataset')
countries = json.loads((root / 'countries.json').read_text())
worldbank = json.loads((root / 'worldbank-2024.json').read_text())[1]
populations = {row['countryiso3code']: row['value'] for row in worldbank if row.get('date') == '2024' and row.get('countryiso3code') and row.get('value') is not None}
with tarfile.open(root / 'tzdata.tar.gz', 'r:gz') as archive:
    zone_lines = archive.extractfile('zone1970.tab').read().decode('utf-8').splitlines()
timezones = {}
for line in zone_lines:
    if not line or line.startswith('#'):
        continue
    codes, _coordinates, zone = line.split('\t')[:3]
    for code in codes.split(','):
        timezones.setdefault(code, []).append(zone)

def emoji(code):
    return ''.join(chr(0x1F1E6 + ord(letter) - ord('A')) for letter in code)

result = []
seen = set()
for source in countries:
    code = source.get('cca2', '')
    if len(code) != 2 or not code.isascii() or not code.isalpha() or code == 'XK':
        continue
    if code in seen:
        raise SystemExit(f'duplicate alpha-2 code: {code}')
    seen.add(code)
    record = {
        'name': {'common': source['name']['common'], 'official': source['name']['official']},
        'cca2': code,
        'cca3': source['cca3'],
        'ccn3': source.get('ccn3', ''),
        'capital': source.get('capital') or [],
        'region': source.get('region', ''),
        'subregion': source.get('subregion', ''),
        'currencies': dict(sorted((source.get('currencies') or {}).items())),
        'languages': dict(sorted((source.get('languages') or {}).items())),
        'area': source.get('area', 0),
        'flag': emoji(code),
        'flag_svg_url': f'https://flagcdn.io/flags/4x3/{code.lower()}.svg',
        'timezones': timezones.get(code, []),
        'latlng': source.get('latlng') or [],
        'borders': source.get('borders') or [],
        'tld': source.get('tld') or [],
    }
    pop = populations.get(source.get('cca3'))
    if pop is not None:
        record['population'] = pop
        record['population_year'] = 2024
    result.append(record)
if len(result) != 249:
    raise SystemExit(f'generated {len(result)} records; expected 249')
result.sort(key=lambda country: country['name']['common'].lower())
Path('src/data/countries.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
print(f'Generated {len(result)} country records')
