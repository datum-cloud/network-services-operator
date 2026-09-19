# envoy_admin_fetch NAMESPACE POD ADMIN_PORT PATH
#
# Prints the admin response body on stdout. Returns non-zero when the admin API
# could not be reached, so a caller can tell a harness failure from a real
# data-plane finding. The proxy container is distroless and has no shell or
# curl, so the admin port is reached by port-forward rather than kubectl exec.
envoy_admin_fetch() {
  _ns="$1"; _pod="$2"; _port="$3"; _path="$4"
  _log=$(mktemp)
  kubectl -n "${_ns}" port-forward "pod/${_pod}" ":${_port}" >"${_log}" 2>&1 &
  _pid=$!
  _local=""
  _i=0
  while [ "${_i}" -lt 40 ]; do
    _local=$(sed -n 's/^Forwarding from 127\.0\.0\.1:\([0-9][0-9]*\).*/\1/p' "${_log}" | head -n1)
    [ -n "${_local}" ] && break
    kill -0 "${_pid}" 2>/dev/null || break
    sleep 0.5
    _i=$((_i + 1))
  done
  if [ -z "${_local}" ]; then
    echo "envoy_admin_fetch: port-forward to ${_ns}/${_pod}:${_port} never became ready" >&2
    sed 's/^/  port-forward: /' "${_log}" >&2
    kill "${_pid}" 2>/dev/null || true
    wait "${_pid}" 2>/dev/null || true
    rm -f "${_log}"
    return 1
  fi
  _rc=0
  if ! _body=$(curl -s --max-time 20 "http://127.0.0.1:${_local}${_path}"); then
    _rc=1
  fi
  kill "${_pid}" 2>/dev/null || true
  wait "${_pid}" 2>/dev/null || true
  rm -f "${_log}"
  if [ "${_rc}" -ne 0 ] || [ -z "${_body}" ]; then
    echo "envoy_admin_fetch: empty or failed response from ${_path} on ${_ns}/${_pod}" >&2
    return 1
  fi
  printf '%s' "${_body}"
}
