#!/usr/bin/env python3
"""Render collision-aborting title-only SQL for the three isolated lab modes.

No database connection, provider changes, auth writes or media proof injection.
The operator reviews/applies the output only in an authorized fixture window.
"""
import argparse
import importlib.util
from pathlib import Path
import time

_spec = importlib.util.spec_from_file_location('ether_mode_fixture', Path(__file__).with_name('mode-fixture.py'))
mode = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(mode)


def seed_sql(manifest, now):
    rows = manifest['profiles']
    keys = ','.join(str(int(p['ratingKey'])) for p in rows)
    tmdbs = ','.join(str(p['tmdb']) for p in rows)
    statements = ["BEGIN;", "SET LOCAL lock_timeout='2s';", "SET LOCAL statement_timeout='10s';",
                  "LOCK TABLE catalog_titles IN SHARE ROW EXCLUSIVE MODE;",
                  "DO $fixture$ BEGIN IF EXISTS (SELECT 1 FROM catalog_titles WHERE key IN ("+keys+") OR (type='movie' AND tmdb_id IN ("+tmdbs+"))) THEN RAISE EXCEPTION 'mode fixture title collision'; END IF; END; $fixture$;"]
    for p in rows:
        title = "'"+p['title'].replace("'", "''")+"'"
        sort_title = "'"+p['title'].lower().replace("'", "''")+"'"
        statements.append("INSERT INTO catalog_titles(key,type,tmdb_id,title,original_title,sort_title,year,released_ts,runtime_sec,language,content_rating,content_rating_rank,browsable,added_at,fetched_at,detail,detail_at,primary_released_ts,year_source,year_checked_at) VALUES("+
                          f"{int(p['ratingKey'])},'movie',{p['tmdb']},{title},{title},{sort_title},2020,1577836800,96,'en','PG',2,1,{now},{now},1,{now},1577836800,'primary',{now});")
    return '\n'.join(statements+["COMMIT;", ""])


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--manifest', type=Path, required=True)
    p.add_argument('--namespace', choices=sorted(mode.NAMESPACES), required=True)
    a = p.parse_args()
    print(seed_sql(mode.read_manifest(a.manifest, a.namespace), int(time.time())), end='')


if __name__ == '__main__':
    main()
