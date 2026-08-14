#!/usr/bin/env bash
#
# Importa el flow definition en un NiFi que ya esté corriendo.
#
# Hace lo mismo que arrastrar un grupo de procesos al lienzo y elegir el
# archivo, pero sin pasar por la interfaz. Si el grupo ya existe lo reemplaza,
# así que sirve tanto para la instalación inicial como para volver a aplicar el
# flow versionado después de tocarlo a mano.
#
# Con --vaciar-colas descarta los FlowFiles que hayan quedado encolados en el
# grupo anterior. Sin esa opción el script se detiene si encuentra datos en
# vuelo, porque reemplazar el grupo los perdería.
#
# Uso:
#     deploy/nifi/importar_flow.sh [--vaciar-colas] [URL_DE_NIFI]
set -euo pipefail

vaciar_colas=false
if [[ "${1:-}" == "--vaciar-colas" ]]; then
    vaciar_colas=true
    shift
fi

api="${1:-http://localhost:8080}/nifi-api"
directorio="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
flow="${directorio}/flow/vialis-ingesta.json"
nombre="Vialis - Ingesta"

if [[ ! -r "${flow}" ]]; then
    echo "no se encuentra ${flow}" >&2
    exit 1
fi

if ! curl -sf "${api}/flow/about" > /dev/null; then
    echo "no hay un NiFi respondiendo en ${api}" >&2
    exit 1
fi

raiz=$(curl -s "${api}/flow/process-groups/root" | jq -r '.processGroupFlow.id')

existente=$(curl -s "${api}/flow/process-groups/${raiz}" \
    | jq -r --arg n "${nombre}" \
        '.processGroupFlow.flow.processGroups[] | select(.component.name == $n) | .id')

if [[ -n "${existente}" ]]; then
    echo "el grupo '${nombre}' ya existe: se detiene y se reemplaza"
    curl -s -X PUT -H 'Content-Type: application/json' \
        -d "{\"id\":\"${existente}\",\"state\":\"STOPPED\",\"disconnectedNodeAcknowledged\":false}" \
        "${api}/flow/process-groups/${existente}" > /dev/null
    sleep 5

    encolados=$(curl -s "${api}/process-groups/${existente}" \
        | jq -r '.status.aggregateSnapshot.flowFilesQueued')

    if [[ "${encolados}" != "0" ]]; then
        if [[ "${vaciar_colas}" != true ]]; then
            echo "el grupo tiene ${encolados} FlowFile(s) encolados." >&2
            echo "revisar las colas en la interfaz, o volver a correr con --vaciar-colas para descartarlos." >&2
            exit 1
        fi
        echo "descartando ${encolados} FlowFile(s) encolados"
        curl -s -X POST \
            "${api}/process-groups/${existente}/empty-all-connections-requests" \
            -H 'Content-Type: application/json' > /dev/null
        sleep 5
    fi

    revision=$(curl -s "${api}/process-groups/${existente}" | jq -r '.revision.version')
    if ! curl -sf -X DELETE \
        "${api}/process-groups/${existente}?version=${revision}&clientId=importar-flow" \
        > /dev/null; then
        echo "no se pudo borrar el grupo anterior; revisarlo en la interfaz" >&2
        exit 1
    fi
fi

codigo=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    "${api}/process-groups/${raiz}/process-groups/upload" \
    -F "file=@${flow}" \
    -F "groupName=${nombre}" \
    -F "positionX=0" \
    -F "positionY=0" \
    -F "clientId=importar-flow" \
    -F "disconnectedNodeAcknowledged=false")

if [[ "${codigo}" != "201" ]]; then
    echo "la importación falló (HTTP ${codigo})" >&2
    exit 1
fi

echo "flow importado en ${api%/nifi-api}/nifi"
echo "los procesadores quedan detenidos: arrancar el grupo que se quiera usar"
