#!/usr/bin/env python3
"""SQL regression tests with session TEMP fixtures; no persistent/source writes.
Run with TEST_DATABASE_URL (or DATABASE_URL) and psql on PATH.
"""
import json
import os
from pathlib import Path
import subprocess
import unittest

URL = os.environ.get("TEST_DATABASE_URL") or os.environ.get("DATABASE_URL")
SETUP = """
CREATE TEMP TABLE capturas_colectivos (
 id_captura bigint, recibido_en timestamptz, cobertura_parcial boolean,
 filas_validas integer, filas_stale integer
);
INSERT INTO capturas_colectivos
SELECT n+1,'2026-10-14T12:00:00Z'::timestamptz+n*interval '1 minute',false,1,0
FROM generate_series(0,4) AS n;
CREATE TEMP TABLE posiciones_colectivos_raw (
 observado_en timestamptz, recibido_en timestamptz, id_vehiculo text,
 id_captura bigint, gcba_route_id text, gcba_direction smallint,
 gtfs_route_id text,gtfs_direction_id smallint,gtfs_shape_id text,gtfs_shape_hash text
);
INSERT INTO posiciones_colectivos_raw VALUES
('2026-10-14T11:59:50Z','2026-10-14T12:00:00Z','synthetic',1,'g',0,'r',0,'s','h'),
('2026-10-14T12:02:20Z','2026-10-14T12:04:00Z','synthetic',5,'g',0,'r',0,'s','h');
"""


@unittest.skipUnless(URL, "set TEST_DATABASE_URL or DATABASE_URL")
class GPSDelaySQLTest(unittest.TestCase):
    def execute(self, script, extra=""):
        query = Path(__file__).with_name(script).read_text().replace("vialis.", "pg_temp.")
        result = subprocess.run(["psql", "-d", URL, "-X", "-qAt", "-v", "ON_ERROR_STOP=1"],
                                input=SETUP + extra + query, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_delay_and_bins(self):
        report = self.execute("audit-bottlenecks-gps-delay.sql")
        sample = next(p for p in report["ages"] if p["sample"] == "all_retained")
        self.assertEqual(sample["positions"], 2)
        self.assertEqual(sample["p50_p90_p95_p99_age_s"][0], 55)
        self.assertEqual(sample["age_0_30s"], 1)
        self.assertEqual(sample["age_60_120s"], 1)
        self.assertEqual(report["arrival_order"]["older_than_previously_received"], 0)

    def test_three_missing_before_fourth_update(self):
        report = self.execute("audit-bottlenecks-update-gaps.sql")
        sample = next(p for p in report["hypothetical_closure_sensitivity"] if p["sample"] == "all_retained")
        self.assertEqual(sample["temporally_compatible_pairs"], 1)
        self.assertEqual(sample["pairs_arriving_after_three_missing_captures"], 1)
        self.assertEqual(sample["pairs_arriving_after_four_missing_captures"], 0)

    def test_four_is_not_a_guarantee_against_late_updates(self):
        extra = """
        INSERT INTO capturas_colectivos VALUES(6,'2026-10-14T12:05:00Z',false,1,0);
        UPDATE posiciones_colectivos_raw SET recibido_en='2026-10-14T12:05:00Z',id_captura=6 WHERE id_captura=5;
        """
        report = self.execute("audit-bottlenecks-update-gaps.sql", extra)
        sample = next(p for p in report["hypothetical_closure_sensitivity"] if p["sample"] == "all_retained")
        self.assertEqual(sample["maximum_missing_complete_captures"], 4)
        self.assertEqual(sample["pairs_arriving_after_four_missing_captures"], 1)

    def test_partial_does_not_increment_counter(self):
        report = self.execute("audit-bottlenecks-update-gaps.sql", "UPDATE capturas_colectivos SET cobertura_parcial=true WHERE id_captura=3;")
        sample = next(p for p in report["hypothetical_closure_sensitivity"] if p["sample"] == "all_retained")
        self.assertEqual(sample["maximum_missing_complete_captures"], 2)
        self.assertEqual(sample["pairs_arriving_after_three_missing_captures"], 0)

    def test_update_on_third_capture_prevents_expiry(self):
        report = self.execute("audit-bottlenecks-update-gaps.sql", "UPDATE posiciones_colectivos_raw SET recibido_en='2026-10-14T12:03:00Z',id_captura=4 WHERE id_captura=5;")
        sample = next(p for p in report["hypothetical_closure_sensitivity"] if p["sample"] == "all_retained")
        self.assertEqual(sample["maximum_missing_complete_captures"], 2)
        self.assertEqual(sample["pairs_arriving_after_three_missing_captures"], 0)


if __name__ == "__main__":
    unittest.main()
