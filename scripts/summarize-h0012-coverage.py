#!/usr/bin/env python3
"""Validate/describe a COMPLETE H0012 NDJSON run; never accept partial batches.
Descriptive volume cuts below are not publication or recurrence thresholds.
No raw vehicle IDs or coordinates are consumed or emitted.
"""
import argparse
from collections import Counter, defaultdict
import json
from pathlib import Path
import statistics


def quantiles(values):
    if not values:
        return None
    values = sorted(values)
    def percentile(p):
        index = (len(values) - 1) * p
        lo = int(index)
        hi = min(lo + 1, len(values) - 1)
        return values[lo] + (values[hi] - values[lo]) * (index - lo)
    return {"p10": percentile(.1), "p50": statistics.median(values), "p90": percentile(.9), "maximum": max(values)}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def summarize(path):
    with Path(path).open() as rows:
        return _summarize(rows)


def _summarize(rows):
    header = footer = None
    expected = {}
    seen = set()
    totals = Counter()
    reasons = Counter()
    support = Counter()
    dates = Counter()
    valid_cell_passages, valid_cell_units, sector_passages = [], [], []
    multiple_date_diversity = Counter()
    target_reports = []
    cuts = (1, 2, 3, 5, 10)
    coverage_fields = {
        "Sectors": "TotalSectors", "ValidPassages": "ValidPassages",
        "IncompleteMeasurements": "IncompleteMeasurements", "BoundaryCensoredMeasurements": "BoundaryCensoredMeasurements",
        "SectorsWithValidPassages": "SectorsWithValidPassages", "SectorsWithMatchedGPS": "SectorsWithMatchedGPS",
        "SectorsWithEvidenceButNoValidPassages": "SectorsWithEvidenceButNoValidPassages",
        "SectorsWithPassagesOnMultipleDates": "SectorsWithPassagesOnMultipleDates",
        "SectorHourPairsWithPassagesOnMultipleDates": "SectorHourPairsWithPassagesOnMultipleDates",
        "CellsWithValidPassages": "CellsWithValidPassages",
    }
    for name in ("Targets", "RawPositions", "EvidenceCells", "MatchedGPSPositions", "SpatiallyUnique", "TemporallyResolved", "Ambiguous", "Unmatched", "Duplicates", *coverage_fields):
        totals[name] = 0
    for number, line in enumerate(rows, 1):
        record = json.loads(line)
        kind = record.get("Type")
        require(footer is None, "records after completion marker")
        if kind == "header":
            require(number == 1 and header is None, "header must be first and unique")
            header = record
            targets = header["Targets"] or []
            expected = {t["LineID"]: t for t in targets}
            require(len(expected) == len(targets), "duplicate manifest target")
            continue
        require(header is not None, "missing header")
        if kind == "complete":
            footer = record["Summary"]
            continue
        require(kind == "analysis", "unknown record type")
        a = record["Analysis"]
        identity = a["LineID"]
        require(identity in expected and identity not in seen, "unexpected/duplicate target")
        t = expected[identity]
        require(a["Version"] == t["Version"] and a["RawPositions"] == t["ExpectedRawPositions"], "manifest changed")
        require(a["Window"] == header["Window"] and a["Policy"] == header["Policy"] and a["NominalMeters"] == header["NominalMeters"], "mixed measurement configuration")
        c = a["Coverage"]
        require(c["TimeZone"] == header["TimeZone"] and c["PassageAttribution"] == "estimated_entry", "mixed temporal attribution")
        require(c["MatchedGPSPositions"] == a["SpatiallyUnique"] + a["TemporallyResolved"], "GPS evidence mismatch")
        require(sum(a.get(name, 0) for name in ("SpatiallyUnique", "TemporallyResolved", "Ambiguous", "Unmatched", "Duplicates")) == a["RawPositions"], "matching population mismatch")
        require(c["StartedMeasurements"] == c["ValidPassages"] + c["IncompleteMeasurements"], "entry accounting mismatch")
        require(sum(c["IncompleteByReason"].values()) == c["IncompleteMeasurements"], "incomplete reasons mismatch")
        require(c["IncompleteByReason"].get("batch_boundary", 0) == c["BoundaryCensoredMeasurements"], "censoring mismatch")
        require(sum(s["Passages"] for s in a["Sectors"]) == c["ValidPassages"], "measurement/coverage mismatch")
        slots = c["Sectors"]
        require([s["SectorIndex"] for s in slots] == list(range(c["TotalSectors"])), "invalid sector layout")
        require(sum(s["ValidPassages"] for s in slots) == c["ValidPassages"], "sector passages mismatch")
        require(sum(s["IncompleteMeasurements"] for s in slots) == c["IncompleteMeasurements"], "sector incomplete mismatch")
        require(sum(s["MatchedGPSPositions"] for s in slots) == c["MatchedGPSPositions"], "sector GPS mismatch")
        require(sum(bool(s["ValidPassages"]) for s in slots) == c["SectorsWithValidPassages"], "valid sector count mismatch")
        require(sum(bool(s["MatchedGPSPositions"]) for s in slots) == c["SectorsWithMatchedGPS"], "GPS sector count mismatch")
        require(sum(s["ValidPassages"] == 0 and bool(s["MatchedGPSPositions"] or s["IncompleteMeasurements"]) for s in slots) == c["SectorsWithEvidenceButNoValidPassages"], "evidence-only sector count mismatch")
        require(sum(len(s["DatesWithPassages"]) > 1 for s in slots) == c["SectorsWithPassagesOnMultipleDates"], "multiple-date sector count mismatch")
        sector_passages.extend(s["ValidPassages"] for s in slots)
        cells = c["Cells"]
        keys = {(s["SectorIndex"], s["Date"], s["Hour"]) for s in cells}
        require(len(keys) == len(cells), "duplicate sector/date/hour cell")
        require(sum(s["ValidPassages"] for s in cells) == c["ValidPassages"], "cell passages mismatch")
        require(sum(s["IncompleteMeasurements"] for s in cells) == c["IncompleteMeasurements"], "cell incomplete mismatch")
        require(sum(s["MatchedGPSPositions"] for s in cells) == c["MatchedGPSPositions"], "cell GPS mismatch")
        pairs = defaultdict(list)
        for cell in cells:
            count = cell["ValidPassages"]
            require(0 <= cell["Hour"] < 24 and 0 <= cell["SectorIndex"] < c["TotalSectors"], "invalid cell")
            require(cell["UniquePassageVehicles"] <= count and (count == 0 or cell["UniquePassageVehicles"] > 0), "invalid unit evidence")
            require(sum(cell["IncompleteByReason"].values()) == cell["IncompleteMeasurements"], "cell reasons mismatch")
            require(cell["InteriorGPSPassages"] + cell["SameIntervalPassages"] + cell["BoundaryOnlyPassages"] == count, "support categories mismatch")
            for name in ("InteriorGPSPassages", "SameIntervalPassages", "BoundaryOnlyPassages"):
                support[name] += cell[name]
            if count:
                valid_cell_passages.append(count)
                valid_cell_units.append(cell["UniquePassageVehicles"])
                dates[cell["Date"]] += count
                pairs[(cell["SectorIndex"], cell["Hour"])].append(cell)
        require(sum(bool(s["ValidPassages"]) for s in cells) == c["CellsWithValidPassages"], "valid cells mismatch")
        multi = [p for p in pairs.values() if len(p) > 1]
        require(len(multi) == c["SectorHourPairsWithPassagesOnMultipleDates"], "multiple date/hour mismatch")
        for pair in multi:
            for n in cuts:
                if all(cell["UniquePassageVehicles"] >= n for cell in pair):
                    multiple_date_diversity[n] += 1
        reasons.update(c["IncompleteByReason"])
        totals["Targets"] += 1
        totals["RawPositions"] += a["RawPositions"]
        totals["EvidenceCells"] += len(cells)
        totals["MatchedGPSPositions"] += c["MatchedGPSPositions"]
        for name in ("SpatiallyUnique", "TemporallyResolved", "Ambiguous", "Unmatched", "Duplicates"):
            totals[name] += a.get(name, 0)
        for name, field in coverage_fields.items():
            totals[name] += c[field]
        seen.add(identity)
        target_reports.append({"LineID": identity, "Line": a["Line"], "Branch": a["Branch"], "Direction": a["Version"]["Direction"],
            "Sectors": c["TotalSectors"], "SectorsWithValidPassages": c["SectorsWithValidPassages"],
            "SectorsWithPassagesOnMultipleDates": c["SectorsWithPassagesOnMultipleDates"],
            "RawPositions": a["RawPositions"], "ValidPassages": c["ValidPassages"], "IncompleteMeasurements": c["IncompleteMeasurements"],
            "CellsWithValidPassages": c["CellsWithValidPassages"]})
    require(header is not None and footer is not None, "incomplete batch: no completion marker")
    require(seen == set(expected), "not all manifest targets were processed")
    require(set(footer) == {"Targets", "RawPositions"} | set(coverage_fields), "completion summary fields mismatch")
    require(all(totals[name] == value for name, value in footer.items()), "completion summary mismatch")
    return {
        "Window": header["Window"], "TimeZone": header["TimeZone"], "NominalMeters": header["NominalMeters"], "Policy": header["Policy"],
        "Totals": dict(totals), "IncompleteByReason": dict(reasons), "PassageSupport": dict(support), "PassagesByLocalDate": dict(sorted(dates.items())),
        "PassagesPerSectorIncludingZeros": quantiles(sector_passages), "PassagesPerValidCell": quantiles(valid_cell_passages), "UniqueVehiclesPerValidCell": quantiles(valid_cell_units),
        "DescriptiveVolumeCuts": [{"n": n, "SectorsWithAtLeastNPassages": sum(v >= n for v in sector_passages),
            "CellsWithAtLeastNPassages": sum(v >= n for v in valid_cell_passages), "CellsWithAtLeastNUniqueVehicles": sum(v >= n for v in valid_cell_units),
            "SectorHourPairsOnMultipleDatesEachWithAtLeastNUniqueVehicles": multiple_date_diversity[n]} for n in cuts],
        "Targets": target_reports,
        "Limitations": ["Descriptive cuts are not publication thresholds", "Route sectors are not unique geographic places", "Incomplete entries are not a true count of actual trips lost", "Repeated passages or dates do not prove recurrent slowness", "No service availability inferred outside observed evidence cells"],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input", type=Path)
    args = parser.parse_args()
    try:
        report = summarize(args.input)
    except (ValueError, KeyError, OSError) as error:
        parser.exit(1, f"Cannot consolidate coverage: {error}\n")
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
