#!/usr/bin/env node

import fs from 'node:fs';

const [primaryPath, comparisonPath, outputPath] = process.argv.slice(2);
if (!primaryPath || !comparisonPath || !outputPath) {
  console.error('uso: node scripts/select-gcba-spatial-matches.mjs MATCHES-800.json MATCHES-500.json SALIDA.csv');
  process.exit(2);
}

const primary = JSON.parse(fs.readFileSync(primaryPath, 'utf8'));
const comparison = JSON.parse(fs.readFileSync(comparisonPath, 'utf8'));
const keyOf = match => `${match.gcbaRouteID}\0${match.gcbaDirection}`;
const comparisonByKey = new Map(comparison.matches.map(match => [keyOf(match), match]));

const columns = [
  'gcba_route_id', 'gcba_direction', 'route_short_names', 'agency_names',
  'observations', 'classification', 'stable_between_samples',
  'gtfs_route_id', 'gtfs_direction_id', 'gtfs_route_short_name',
  'coverage_100m', 'coverage_200m', 'shape_coverage_20_bins', 'median_distance_m', 'p95_distance_m',
  'direction_agreement', 'direction_pairs', 'score', 'margin_to_second',
  'second_gtfs_route_id', 'second_gtfs_direction_id', 'second_gtfs_route_short_name',
  'second_score',
];

function encodeCSV(rows) {
  const escape = value => {
    const text = String(value ?? '');
    return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text;
  };
  return `${columns.join(',')}\n${rows.map(row => columns.map(column => escape(row[column])).join(',')).join('\n')}\n`;
}

const counts = {};
const rows = primary.matches.map(match => {
  const top = match.spatialRanking[0];
  const second = match.spatialRanking[1];
  const other = comparisonByKey.get(keyOf(match));
  const comparisonTop = other?.spatialRanking[0];
  const stable = Boolean(top && comparisonTop && top.routeID === comparisonTop.routeID && top.direction === comparisonTop.direction);
  const margin = top ? top.score - (second?.score ?? 0) : 0;

  let classification;
  if (!top || top.coverage200m < 0.30) classification = 'no_spatial_match';
  else if (match.observations < 100 || top.directionPairs < 30) classification = 'insufficient_observations';
  else if (
    stable
    && top.coverage200m >= 0.90
    && top.coverage100m >= 0.70
    && top.shapeCoverage20Bins >= 0.80
    && top.directionAgreement >= 0.80
    && margin >= 0.10
  ) classification = 'high_confidence';
  else if (margin < 0.10) classification = 'ambiguous';
  else classification = 'review';
  counts[classification] = (counts[classification] ?? 0) + 1;

  return {
    gcba_route_id: match.gcbaRouteID,
    gcba_direction: match.gcbaDirection,
    route_short_names: match.routeShortNames.join(' | '),
    agency_names: match.agencyNames.join(' | '),
    observations: match.observations,
    classification,
    stable_between_samples: stable,
    gtfs_route_id: top?.routeID,
    gtfs_direction_id: top?.direction,
    gtfs_route_short_name: top?.shortName,
    coverage_100m: top?.coverage100m,
    coverage_200m: top?.coverage200m,
    shape_coverage_20_bins: top?.shapeCoverage20Bins,
    median_distance_m: top?.medianDistanceM,
    p95_distance_m: top?.p95DistanceM,
    direction_agreement: top?.directionAgreement,
    direction_pairs: top?.directionPairs,
    score: top?.score,
    margin_to_second: top ? Number(margin.toFixed(6)) : null,
    second_gtfs_route_id: second?.routeID,
    second_gtfs_direction_id: second?.direction,
    second_gtfs_route_short_name: second?.shortName,
    second_score: second?.score,
  };
});

const order = { high_confidence: 0, review: 1, ambiguous: 2, insufficient_observations: 3, no_spatial_match: 4 };
rows.sort((a, b) => order[a.classification] - order[b.classification] || a.route_short_names.localeCompare(b.route_short_names, 'es', { numeric: true }) || Number(a.gcba_direction) - Number(b.gcba_direction) || a.gcba_route_id.localeCompare(b.gcba_route_id, 'es', { numeric: true }));
fs.writeFileSync(outputPath, encodeCSV(rows));
console.log(JSON.stringify({ identities: rows.length, classifications: counts, output: outputPath }, null, 2));
