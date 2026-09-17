package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/paulmach/orb/geojson"
)

type viewerFeatureCollection struct {
	Type     string          `json:"type"`
	Features []viewerFeature `json:"features"`
}

type viewerFeature struct {
	Type       string          `json:"type"`
	Geometry   json.RawMessage `json:"geometry"`
	Properties map[string]any  `json:"properties"`
}

func exportViewerFiles(
	ctx context.Context,
	transaction pgx.Tx,
	directory string,
	segments []trafficSegment,
) error {
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create viewer directory: %w", err)
	}
	if err := exportTrafficGeoJSON(directory, segments); err != nil {
		return err
	}
	if err := exportGraphGeoJSON(ctx, transaction, directory); err != nil {
		return err
	}
	if err := writeTextFile(filepath.Join(directory, "index.html"), viewerHTML); err != nil {
		return fmt.Errorf("write viewer HTML: %w", err)
	}
	return nil
}

func exportTrafficGeoJSON(directory string, segments []trafficSegment) error {
	collection := geojson.NewFeatureCollection()
	for index, segment := range segments {
		feature := geojson.NewFeature(segment.Geometry)
		feature.ID = index + 1
		feature.Properties = geojson.Properties{
			"roadType":        segment.RoadType,
			"roadCategory":    segment.RoadCategory,
			"roadSubcategory": segment.RoadSubcategory,
			"roadCoverage":    segment.RoadCoverage,
			"speedKph":        segment.SpeedKPH,
			"hasSpeed":        segment.HasSpeed,
			"closure":         segment.Closure,
		}
		collection.Append(feature)
	}
	if err := writeJSONFile(filepath.Join(directory, "traffic.geojson"), collection); err != nil {
		return fmt.Errorf("write viewer traffic GeoJSON: %w", err)
	}
	return nil
}

func exportGraphGeoJSON(ctx context.Context, transaction pgx.Tx, directory string) error {
	rows, err := transaction.Query(ctx, viewerGraphSQL)
	if err != nil {
		return fmt.Errorf("query viewer graph: %w", err)
	}
	defer rows.Close()

	collection := viewerFeatureCollection{Type: "FeatureCollection"}
	for rows.Next() {
		var (
			id              int64
			roadType        string
			matched         bool
			estimated       bool
			estimatedSpeed  *float64
			estimateSamples *int
			distanceMeters  *float64
			angleDegrees    *float64
			trafficRoadType *string
			roadCoverage    *string
			geometry        []byte
		)
		if err := rows.Scan(
			&id,
			&roadType,
			&matched,
			&estimated,
			&estimatedSpeed,
			&estimateSamples,
			&distanceMeters,
			&angleDegrees,
			&trafficRoadType,
			&roadCoverage,
			&geometry,
		); err != nil {
			return fmt.Errorf("scan viewer graph: %w", err)
		}
		properties := map[string]any{
			"idCalle":   id,
			"roadType":  roadType,
			"matched":   matched,
			"estimated": estimated,
		}
		if estimatedSpeed != nil {
			properties["estimatedSpeedKph"] = *estimatedSpeed
		}
		if estimateSamples != nil {
			properties["estimateSamples"] = *estimateSamples
		}
		if distanceMeters != nil {
			properties["distanceMeters"] = *distanceMeters
		}
		if angleDegrees != nil {
			properties["angleDegrees"] = *angleDegrees
		}
		if trafficRoadType != nil {
			properties["trafficRoadType"] = *trafficRoadType
		}
		if roadCoverage != nil {
			properties["trafficRoadCoverage"] = *roadCoverage
		}
		collection.Features = append(collection.Features, viewerFeature{
			Type:       "Feature",
			Geometry:   geometry,
			Properties: properties,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read viewer graph: %w", err)
	}
	if err := writeJSONFile(filepath.Join(directory, "graph.geojson"), collection); err != nil {
		return fmt.Errorf("write viewer graph GeoJSON: %w", err)
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(value); err != nil {
		file.Close()
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func writeTextFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

const viewerGraphSQL = `
SELECT
    c.id_calle,
    c.tipo,
    m.traffic_id IS NOT NULL AS matched,
    m.traffic_id IS NULL AND e.estimated_usable AS estimated,
    e.estimated_speed_kph,
    e.sample_count,
    m.distance_meters,
    m.angle_degrees,
    m.traffic_road_type,
    m.traffic_road_coverage,
    ST_AsGeoJSON(ST_SimplifyPreserveTopology(c.geom, 0.000005), 6)::bytea
FROM tomtom_spike_matches m
JOIN tomtom_spike_estimates e USING (id_calle)
JOIN vialis.calles c USING (id_calle)
ORDER BY c.id_calle;
`

const viewerHTML = `<!doctype html>
<html lang="es">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Vialis — matching TomTom / OSM</title>
  <link href="https://unpkg.com/maplibre-gl@4.7.1/dist/maplibre-gl.css" rel="stylesheet">
  <script src="https://unpkg.com/maplibre-gl@4.7.1/dist/maplibre-gl.js"></script>
  <style>
    html, body, #map { height: 100%; margin: 0; }
    body { font-family: system-ui, sans-serif; }
    .panel { position: absolute; z-index: 2; top: 12px; left: 12px; width: 290px;
      background: rgba(255,255,255,.96); border-radius: 8px; padding: 12px;
      box-shadow: 0 2px 12px #0004; font-size: 13px; }
    .panel h1 { font-size: 16px; margin: 0 0 8px; }
    .row { display: flex; align-items: center; gap: 7px; margin: 5px 0; }
    .swatch { width: 18px; height: 4px; display: inline-block; }
    #stats { margin-top: 8px; color: #444; line-height: 1.35; }
    .maplibregl-popup-content { font: 12px/1.4 system-ui, sans-serif; }
  </style>
</head>
<body>
<div id="map"></div>
<div class="panel">
  <h1>Matching TomTom / vialis.calles</h1>
  <label class="row"><input type="checkbox" data-layer="graph-unmatched" checked>
    <span class="swatch" style="background:#e31a1c"></span>Sin tráfico ni estimación</label>
  <label class="row"><input type="checkbox" data-layer="graph-estimated" checked>
    <span class="swatch" style="background:#ffb000"></span>Velocidad estimada</label>
  <label class="row"><input type="checkbox" data-layer="graph-matched" checked>
    <span class="swatch" style="background:#33a02c"></span>Con match</label>
  <label class="row"><input type="checkbox" data-layer="tomtom-flow" checked>
    <span class="swatch" style="background:#1f78b4"></span>Geometría TomTom</label>
  <div id="stats">Cargando capas…</div>
</div>
<script>
const map = new maplibregl.Map({
  container: 'map',
  center: [-58.44, -34.615],
  zoom: 10.7,
  style: {
    version: 8,
    sources: { osm: { type: 'raster', tiles: ['https://tile.openstreetmap.org/{z}/{x}/{y}.png'], tileSize: 256,
      attribution: '© OpenStreetMap contributors' } },
    layers: [{ id: 'osm', type: 'raster', source: 'osm' }]
  }
});
map.addControl(new maplibregl.NavigationControl(), 'top-right');

map.on('load', async () => {
  try {
    const [graph, traffic, report] = await Promise.all([
      fetch('graph.geojson').then(r => r.json()),
      fetch('traffic.geojson').then(r => r.json()),
      fetch('report.json').then(r => r.json())
    ]);
    map.addSource('graph', { type: 'geojson', data: graph });
    map.addSource('traffic', { type: 'geojson', data: traffic });
    map.addLayer({ id: 'graph-matched', type: 'line', source: 'graph',
      filter: ['==', ['get', 'matched'], true],
      paint: { 'line-color': '#33a02c', 'line-width': 2, 'line-opacity': .75 } });
    map.addLayer({ id: 'graph-estimated', type: 'line', source: 'graph',
      filter: ['all', ['==', ['get', 'matched'], false], ['==', ['get', 'estimated'], true]],
      paint: { 'line-color': '#ffb000', 'line-width': 2.2, 'line-opacity': .85 } });
    map.addLayer({ id: 'graph-unmatched', type: 'line', source: 'graph',
      filter: ['all', ['==', ['get', 'matched'], false], ['==', ['get', 'estimated'], false]],
      paint: { 'line-color': '#e31a1c', 'line-width': 2.4, 'line-opacity': .9 } });
    map.addLayer({ id: 'tomtom-flow', type: 'line', source: 'traffic',
      paint: { 'line-color': '#1f78b4', 'line-width': 1.2, 'line-opacity': .65 } });

    const result = report.results[0], matching = result.graphMatch;
    document.getElementById('stats').innerHTML =
      '<b>' + result.tileCount + '</b> tiles · <b>' + matching.graphEdges.toLocaleString('es-AR') + '</b> aristas<br>' +
      '<b>' + matching.matchedLengthPercent.toFixed(1) + '%</b> directa · ' +
      '<b>' + matching.nearbyEstimation.directOrEstimatedLengthPercent.toFixed(1) + '%</b> directa o estimada<br>' +
      '<b>' + matching.nearbyEstimation.largestOfOriginalVertexPercent.toFixed(1) + '%</b> en la componente estimada principal';

    for (const id of ['graph-unmatched', 'graph-estimated', 'graph-matched', 'tomtom-flow']) {
      map.on('click', id, event => {
        const properties = event.features[0].properties;
        const lines = Object.entries(properties).map(([key, value]) => '<b>' + key + '</b>: ' + value).join('<br>');
        new maplibregl.Popup().setLngLat(event.lngLat).setHTML(lines).addTo(map);
      });
      map.on('mouseenter', id, () => { map.getCanvas().style.cursor = 'pointer'; });
      map.on('mouseleave', id, () => { map.getCanvas().style.cursor = ''; });
    }
  } catch (error) {
    document.getElementById('stats').textContent = 'Error cargando datos: ' + error.message;
    console.error(error);
  }
});

document.querySelectorAll('input[data-layer]').forEach(input => {
  input.addEventListener('change', () => {
    if (map.getLayer(input.dataset.layer)) {
      map.setLayoutProperty(input.dataset.layer, 'visibility', input.checked ? 'visible' : 'none');
    }
  });
});
</script>
</body>
</html>
`
