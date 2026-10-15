#!/usr/bin/env python3
"""Standard-library regression tests for complete-only coverage consolidation."""
import copy
import json
from pathlib import Path
import runpy
import tempfile
import unittest

summarize = runpy.run_path(str(Path(__file__).with_name("summarize-h0012-coverage.py")))["summarize"]


def records():
    version = {"RouteID": "r", "Direction": 0, "ShapeID": "s", "ShapeHash": "h"}
    window = {"From": "2026-10-10T12:00:00Z", "To": "2026-10-10T14:00:00Z"}
    policy = {"MaximumGapSeconds": 180, "MaximumDistanceMeters": 100, "MaximumSpeedKPH": 100}
    zone = "America/Argentina/Buenos_Aires"
    coverage = {
        "TimeZone": zone, "PassageAttribution": "estimated_entry", "TotalSectors": 1,
        "SectorsWithMatchedGPS": 1, "SectorsWithValidPassages": 1,
        "SectorsWithEvidenceButNoValidPassages": 0, "SectorsWithPassagesOnMultipleDates": 0,
        "SectorHourPairsWithPassagesOnMultipleDates": 0, "CellsWithValidPassages": 1,
        "MatchedGPSPositions": 2, "StartedMeasurements": 1, "ValidPassages": 1,
        "IncompleteMeasurements": 0, "BoundaryCensoredMeasurements": 0, "IncompleteByReason": {},
        "Sectors": [{"SectorIndex": 0, "MatchedGPSPositions": 2, "UniqueGPSVehicles": 1,
                     "ValidPassages": 1, "UniquePassageVehicles": 1, "IncompleteMeasurements": 0,
                     "BoundaryCensoredMeasurements": 0, "DatesWithPassages": ["2026-10-10"]}],
        "Cells": [{"SectorIndex": 0, "Date": "2026-10-10", "Hour": 9, "MatchedGPSPositions": 2,
                   "UniqueGPSVehicles": 1, "ValidPassages": 1, "UniquePassageVehicles": 1,
                   "InteriorGPSPassages": 1, "SameIntervalPassages": 0, "BoundaryOnlyPassages": 0,
                   "IncompleteMeasurements": 0, "BoundaryCensoredMeasurements": 0, "IncompleteByReason": {}}],
    }
    footer = {"Targets": 1, "RawPositions": 2, "Sectors": 1}
    for name in ("ValidPassages", "IncompleteMeasurements", "BoundaryCensoredMeasurements", "SectorsWithValidPassages",
                 "SectorsWithMatchedGPS", "SectorsWithEvidenceButNoValidPassages", "SectorsWithPassagesOnMultipleDates",
                 "SectorHourPairsWithPassagesOnMultipleDates", "CellsWithValidPassages"):
        footer[name] = coverage[name]
    return [
        {"Type": "header", "Window": window, "Policy": policy, "NominalMeters": 500, "TimeZone": zone,
         "Targets": [{"LineID": 1, "Version": version, "ExpectedRawPositions": 2}]},
        {"Type": "analysis", "Analysis": {"LineID": 1, "Version": version, "RawPositions": 2, "Window": window,
         "Policy": policy, "NominalMeters": 500, "Line": "L", "Branch": "R", "SpatiallyUnique": 2,
         "TemporallyResolved": 0, "Sectors": [{"Passages": 1}], "Coverage": coverage}},
        {"Type": "complete", "Summary": footer},
    ]


class CoverageSummaryTest(unittest.TestCase):
    def evaluate(self, rows):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.ndjson"
            path.write_text("".join(json.dumps(row) + "\n" for row in rows))
            return summarize(path)

    def test_complete(self):
        result = self.evaluate(records())
        self.assertEqual(result["Totals"]["ValidPassages"], 1)
        self.assertEqual(result["UniqueVehiclesPerValidCell"]["p50"], 1)
        self.assertEqual(result["DescriptiveVolumeCuts"][0]["CellsWithAtLeastNPassages"], 1)

    def test_partial(self):
        with self.assertRaisesRegex(ValueError, "completion marker"):
            self.evaluate(records()[:-1])

    def test_duplicate_target(self):
        rows = records()
        rows.insert(2, copy.deepcopy(rows[1]))
        with self.assertRaisesRegex(ValueError, "duplicate target"):
            self.evaluate(rows)

    def test_wrong_footer(self):
        rows = records()
        rows[-1]["Summary"]["RawPositions"] = 3
        with self.assertRaisesRegex(ValueError, "summary mismatch"):
            self.evaluate(rows)

    def test_missing_summary_fields(self):
        rows = records()
        del rows[-1]["Summary"]["RawPositions"]
        with self.assertRaisesRegex(ValueError, "summary fields"):
            self.evaluate(rows)

    def test_complete_empty_selection(self):
        rows = records()
        rows[0]["Targets"] = None
        rows[-1]["Summary"] = {name: 0 for name in rows[-1]["Summary"]}
        result = self.evaluate([rows[0], rows[-1]])
        self.assertEqual(result["Targets"], [])
        self.assertEqual(result["Totals"]["RawPositions"], 0)
        self.assertIsNone(result["PassagesPerValidCell"])

    def test_bad_unit_evidence(self):
        rows = records()
        rows[1]["Analysis"]["Coverage"]["Cells"][0]["UniquePassageVehicles"] = 2
        with self.assertRaisesRegex(ValueError, "unit evidence"):
            self.evaluate(rows)

    def test_support_mismatch(self):
        rows = records()
        rows[1]["Analysis"]["Coverage"]["Cells"][0]["InteriorGPSPassages"] = 0
        with self.assertRaisesRegex(ValueError, "support categories"):
            self.evaluate(rows)

    def test_config_mismatch(self):
        rows = records()
        rows[1]["Analysis"]["Coverage"]["TimeZone"] = "UTC"
        with self.assertRaisesRegex(ValueError, "temporal attribution"):
            self.evaluate(rows)

    def test_record_after_footer(self):
        rows = records()
        rows.append(copy.deepcopy(rows[1]))
        with self.assertRaisesRegex(ValueError, "after completion"):
            self.evaluate(rows)


if __name__ == "__main__":
    unittest.main()
