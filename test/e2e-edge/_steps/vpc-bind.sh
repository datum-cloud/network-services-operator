# vpc_bind_wait ENVOY_NS POD_SELECTOR ADMIN_PORT CLUSTER_MATCH WANT_ENDPOINTS WANT_DEVICE TIMEOUT
#
# Waits until every Ready edge proxy pod carries exactly one cluster whose name
# contains CLUSTER_MATCH, with endpoints WANT_ENDPOINTS (space separated, any
# order) and an outbound socket bound to WANT_DEVICE ("none" for no bind).
# Returns non-zero after TIMEOUT seconds, printing each pod's last state.
#
# Requires envoy_admin_fetch from envoy-admin.sh.
vpc_bind_wait() {
  _ns="$1"; _selector="$2"; _port="$3"; _match="$4"
  _want_endpoints=$(printf '%s\n' $5 | sort | paste -sd, -)
  _want_device="$6"; _timeout="$7"
  _want="${_want_endpoints} ${_want_device}"
  _start=$(date +%s)
  _dump=$(mktemp)
  while :; do
    _elapsed=$(( $(date +%s) - _start ))
    _pods=$(kubectl -n "${_ns}" get pods -l "${_selector}" \
      -o go-template='{{range $p := .items}}{{if not $p.metadata.deletionTimestamp}}{{range $p.status.conditions}}{{if (and (eq .type "Ready") (eq .status "True"))}}{{$p.metadata.name}}{{"\n"}}{{end}}{{end}}{{end}}{{end}}' \
      2>/dev/null || true)
    _all="${_pods:+yes}"
    _report=""
    for _pod in ${_pods}; do
      if ! envoy_admin_fetch "${_ns}" "${_pod}" "${_port}" "/config_dump?include_eds" >"${_dump}"; then
        _state="unreachable"
      else
        _state=$(MATCH="${_match}" python3 -c '
import base64, json, os, sys

match = os.environ["MATCH"]
try:
    dump = json.load(open(sys.argv[1]))
except Exception:
    print("unreadable"); sys.exit()

devices, endpoints = {}, {}
for config in dump.get("configs", []):
    kind = config.get("@type", "")
    if "ClustersConfigDump" in kind:
        for entry in config.get("dynamic_active_clusters", []):
            cluster = entry.get("cluster", {})
            if match not in cluster.get("name", ""):
                continue
            device = "none"
            for option in cluster.get("upstream_bind_config", {}).get("socket_options", []):
                if int(option.get("level", 0)) == 1 and int(option.get("name", 0)) == 25:
                    device = base64.b64decode(option.get("buf_value", "")).decode().rstrip("\0")
            devices[cluster["name"]] = device
    elif "EndpointsConfigDump" in kind:
        for entry in config.get("dynamic_endpoint_configs", []):
            assignment = entry.get("endpoint_config", {})
            if match not in assignment.get("cluster_name", ""):
                continue
            addresses = endpoints.setdefault(assignment["cluster_name"], [])
            for group in assignment.get("endpoints", []):
                for endpoint in group.get("lb_endpoints", []):
                    addresses.append(endpoint["endpoint"]["address"]["socket_address"]["address"])

if len(devices) != 1:
    print("clusters=%d" % len(devices)); sys.exit()
name, device = next(iter(devices.items()))
print("%s %s" % (",".join(sorted(endpoints.get(name, []))) or "-", device))
' "${_dump}")
      fi
      _report="${_report} ${_pod}=[${_state}]"
      [ "${_state}" = "${_want}" ] || _all=""
    done
    if [ -n "${_all}" ]; then
      rm -f "${_dump}"
      echo "OK after ${_elapsed}s: every proxy pod carries [${_want}]:${_report}"
      return 0
    fi
    if [ "${_elapsed}" -ge "${_timeout}" ]; then
      rm -f "${_dump}"
      echo "ERROR: after ${_elapsed}s the proxy pods do not carry [${_want}]:${_report:- no Ready proxy pod}"
      return 1
    fi
    echo "${_elapsed}s: waiting for [${_want}]:${_report:- no Ready proxy pod}"
    sleep 2
  done
}
