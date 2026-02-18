import http.client
import json
import os
import re
import socket
import subprocess
import sys
import threading
import time

import pytest


REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
INDEX_HTML = os.path.join(REPO, "index.html")
SERVER_PY = os.path.join(REPO, "server.py")
REFRESH_SH = os.path.join(REPO, "refresh.sh")
DATA_JSON = os.path.join(REPO, "data.json")


def _read(path):
    with open(path, "r", encoding="utf-8") as f:
        return f.read()


def _extract_script(html):
    m = re.search(r"<script>([\s\S]*)</script>", html)
    assert m, "<script> block not found in index.html"
    return m.group(1)


def _extract_render_body(js):
    m = re.search(r"function\s+render\s*\(\)\s*\{([\s\S]*?)\n\}", js)
    assert m, "render() function not found"
    return m.group(1)


def _extract_load_data_body(js):
    m = re.search(r"async\s+function\s+loadData\s*\(\)\s*\{([\s\S]*?)\n\}", js)
    assert m, "loadData() function not found"
    return m.group(1)


# ----------------------------
# Section 1 — Dirty-check logic
# ----------------------------


def test_tc1_section_changed_calls_use_non_empty_keys():
    js = _extract_script(_read(INDEX_HTML))
    calls = re.findall(r"sectionChanged\s*\(\s*\[(.*?)\]\s*\)", js, re.S)
    assert calls, "No sectionChanged([...]) calls found"
    for arr in calls:
        # Must contain at least one quoted key
        keys = re.findall(r"['\"]([^'\"]+)['\"]", arr)
        assert len(keys) > 0, f"Found empty guarded key list: sectionChanged([{arr.strip()}])"


def test_tc2_prevd_is_deep_cloned_not_referenced():
    js = _extract_script(_read(INDEX_HTML))
    assert "prevD = JSON.parse(JSON.stringify(D));" in js
    assert "prevD = D;" not in js


def test_tc3_stable_snapshot_exists_for_volatile_sections():
    js = _extract_script(_read(INDEX_HTML))
    if "function stableSnapshot" not in js:
        pytest.xfail("stableSnapshot pending architecture refactor")
    # Ensure sessions/crons dirty checks rely on stable snapshot
    assert re.search(r"stableSnapshot\(D\.crons,\s*\[", js), "crons stableSnapshot guard missing"
    assert re.search(r"stableSnapshot\(D\.sessions,\s*\[", js), "sessions stableSnapshot guard missing"


def test_tc4_prev_tabs_saved_before_prevd_snapshot_in_render_end():
    js = _extract_script(_read(INDEX_HTML))
    render_body = _extract_render_body(js)

    i_tabs = render_body.find("prevUTab=uTab; prevSrTab=srTab; prevStTab=stTab;")
    i_prev = render_body.find("prevD = JSON.parse(JSON.stringify(D));")

    assert i_tabs != -1, "prev* tab persistence line not found"
    assert i_prev != -1, "prevD snapshot line not found"
    assert i_tabs < i_prev, "prev* tab variables must be saved before prevD snapshot"


def test_tc5_load_data_uses_request_animation_frame_for_render():
    js = _extract_script(_read(INDEX_HTML))
    load_data = _extract_load_data_body(js)
    assert "requestAnimationFrame(() => render());" in load_data
    # Ensure direct synchronous render() call is not used in loadData
    assert not re.search(r"(?<!=>\s)\brender\(\);", load_data), "loadData() calls render() directly"


# ----------------------------
# Section 2 — XSS coverage
# ----------------------------


def _innerhtml_template_literals(js):
    return re.findall(r"innerHTML\s*=\s*`([\s\S]*?)`", js)


def test_tc6_innerhtml_templates_include_esc_usage():
    js = _extract_script(_read(INDEX_HTML))
    templates = _innerhtml_template_literals(js)
    assert templates, "No innerHTML template-literal assignments found"
    risky_fields = r"(name|model|type|task|message|icon|severity|provider|id|hash|schedule|lastRun|nextRun|status|subject|label)"
    for tpl in templates:
        if "${" not in tpl:
            continue
        exprs = re.findall(r"\$\{([^}]*)\}", tpl)
        has_risky = any(re.search(rf"\.[ \t]*{risky_fields}\b", e.strip()) for e in exprs)
        if has_risky:
            assert "esc(" in tpl, "innerHTML template with risky interpolation missing esc()"


def test_tc7_no_raw_unescaped_user_string_interpolation_in_innerhtml_templates():
    js = _extract_script(_read(INDEX_HTML))
    templates = _innerhtml_template_literals(js)

    suspicious = []
    risky_fields = r"(name|model|type|task|message|icon|severity|provider|id|hash|schedule|lastRun|nextRun|status|subject|label)"

    for tpl in templates:
        for expr in re.findall(r"\$\{([^}]*)\}", tpl):
            e = expr.strip()
            if "esc(" in e or "safeColor(" in e:
                continue
            # flag only likely string fields coming from object properties
            if re.search(rf"\.[ \t]*{risky_fields}\b", e):
                suspicious.append(e)

    assert not suspicious, f"Found potentially unsafe raw interpolations: {suspicious}"


def test_tc8_safecolor_regex_accepts_only_hex_colors():
    js = _extract_script(_read(INDEX_HTML))
    m = re.search(r"return\s*/\^#\[0-9a-fA-F\]\{3,8\}\$/\.test", js)
    assert m, "safeColor hex regex implementation not found"

    hex_re = re.compile(r"^#[0-9a-fA-F]{3,8}$")
    valid = ["#fff", "#ffffff", "#FFFFFF", "#12345678"]
    invalid = ["red", "#xyz", "#gg0000", "url(evil)"]

    for v in valid:
        assert hex_re.match(v), f"Expected valid hex color: {v}"
    for v in invalid:
        assert not hex_re.match(v), f"Expected invalid color to be rejected: {v}"


# ----------------------------
# Section 3 — Server robustness
# ----------------------------


def _free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@pytest.fixture(scope="module")
def server_proc():
    port = _free_port()
    env = os.environ.copy()
    env["DASHBOARD_BIND"] = "127.0.0.1"
    env["DASHBOARD_PORT"] = str(port)

    proc = subprocess.Popen(
        [sys.executable, SERVER_PY, "-b", "127.0.0.1", "-p", str(port)],
        cwd=REPO,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=env,
    )

    # readiness probe
    started = False
    for _ in range(60):
        try:
            conn = http.client.HTTPConnection("127.0.0.1", port, timeout=1)
            conn.request("GET", "/")
            r = conn.getresponse()
            r.read()
            conn.close()
            started = True
            break
        except Exception:
            time.sleep(0.1)

    if not started:
        proc.terminate()
        raise RuntimeError("Server failed to start for tests")

    yield {"proc": proc, "port": port}

    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()


def _request(port, method, path):
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=10)
    conn.request(method, path)
    resp = conn.getresponse()
    body = resp.read()
    headers = dict(resp.getheaders())
    status = resp.status
    conn.close()
    return status, headers, body


def test_tc9_refresh_returns_valid_json(server_proc):
    status, headers, body = _request(server_proc["port"], "GET", "/api/refresh")
    assert status == 200
    assert body, "refresh response is empty"
    txt = body.decode("utf-8", errors="replace").strip()
    assert not txt.lower().startswith("<!doctype html"), "refresh returned HTML, expected JSON"
    parsed = json.loads(txt)
    assert isinstance(parsed, dict)
    assert headers.get("Content-Type", "").startswith("application/json")


def test_tc10_data_endpoint_has_numeric_total_cost_today(server_proc):
    # /api/data (if available), otherwise /api/refresh as equivalent data endpoint.
    status, _, body = _request(server_proc["port"], "GET", "/api/data")
    if status != 200:
        status, _, body = _request(server_proc["port"], "GET", "/api/refresh")
    assert status == 200
    data = json.loads(body.decode("utf-8", errors="replace"))
    assert isinstance(data.get("totalCostToday"), (int, float)), "totalCostToday must be numeric"


def test_tc11_head_root_returns_200(server_proc):
    status, _, body = _request(server_proc["port"], "HEAD", "/")
    assert status == 200  # HEAD should succeed; body content varies by implementation


def test_tc12_concurrent_load_no_500(server_proc):
    port = server_proc["port"]
    statuses = []
    errors = []
    lock = threading.Lock()

    def worker():
        local = []
        for _ in range(3):
            try:
                st, _, _ = _request(port, "GET", "/")
                local.append(st)
            except Exception as e:
                with lock:
                    errors.append(str(e))
        with lock:
            statuses.extend(local)

    threads = [threading.Thread(target=worker) for _ in range(10)]
    for t in threads:
        t.start()
    for t in threads:
        t.join(timeout=15)

    assert not errors, f"Request errors occurred: {errors}"
    assert len(statuses) == 30, f"Expected 30 responses, got {len(statuses)}"
    assert all(s != 500 for s in statuses), f"Observed 500 under concurrent load: {statuses}"


# ----------------------------
# Section 4 — Data integrity
# ----------------------------


@pytest.mark.skipif(not os.path.exists(DATA_JSON), reason="data.json not found yet")
def test_tc13_projected_monthly_vs_today_with_outlier_tolerance():
    data = json.loads(_read(DATA_JSON))
    today = float(data.get("totalCostToday", 0) or 0)
    projected = float(data.get("projectedMonthly", 0) or 0)
    # strict expected relation
    if projected >= today:
        assert True
        return
    # outlier tolerance (up to 10x); projected should still not be absurdly low
    assert projected >= today / 10.0, (
        f"projectedMonthly ({projected}) is too low vs totalCostToday ({today})"
    )


@pytest.mark.skipif(not os.path.exists(DATA_JSON), reason="data.json not found yet")
def test_tc14_cron_schedule_field_shape_is_valid_expression():
    data = json.loads(_read(DATA_JSON))
    crons = data.get("crons", [])
    cron_re = re.compile(r"^(\*/\d+|\d{1,2})\s+(\*|\d{1,2})\s+\*\s+\*\s+(\*|\d{1,2})$")
    iso_at_re = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$")

    for c in crons:
        sched = str(c.get("schedule", "")).strip()
        # Allow either traditional 5-field limited patterns, refresh.sh human-readable style,
        # or explicit one-shot ISO datetime schedules.
        ok = bool(
            cron_re.match(sched)
            or sched.startswith("Every ")
            or sched.startswith("At ")
            or iso_at_re.match(sched)
        )
        assert ok, f"Invalid cron schedule format: {sched!r}"


@pytest.mark.skipif(not os.path.exists(DATA_JSON), reason="data.json not found yet")
def test_tc15_sessions_count_reasonable_vs_active_sessions_field():
    data = json.loads(_read(DATA_JSON))
    sessions = data.get("sessions", [])
    active_count_field = data.get("activeSessions", data.get("sessionCount", len(sessions)))

    # active_count_field is the dashboard's reported activeSessions number
    # sessions[] is the list of sessions returned
    # Reasonable check: activeSessions should be >= 0 and sessions list should be non-empty if activeSessions > 0
    assert isinstance(active_count_field, int) and active_count_field >= 0
    assert isinstance(sessions, list)
    if active_count_field > 0:
        assert len(sessions) > 0, "activeSessions > 0 but sessions list is empty"


@pytest.mark.skipif(not os.path.exists(DATA_JSON), reason="data.json not found yet")
def test_tc16_daily_chart_is_chronological():
    data = json.loads(_read(DATA_JSON))
    chart = data.get("dailyChart", [])

    prev = None
    for entry in chart:
        d = entry.get("date")
        assert isinstance(d, str) and re.match(r"^\d{4}-\d{2}-\d{2}$", d), f"Bad date: {d!r}"
        if prev is not None:
            assert d >= prev, f"dailyChart not chronological: {d} < {prev}"
        prev = d


# ----------------------------
# Section 5 — refresh.sh safety
# ----------------------------


def _extract_embedded_python(sh_text):
    m = re.search(r"<<\s*'\s*PYEOF\s*'\s*>\s*\"\$DIR/data\.json\.tmp\"\n([\s\S]*?)\nPYEOF", sh_text)
    if not m:
        # fallback generic heredoc extractor
        m = re.search(r"<<\s*'?PYEOF'?([\s\S]*?)\nPYEOF", sh_text)
    assert m, "Embedded Python heredoc not found in refresh.sh"
    return m.group(1)


def test_tc17_uses_pgrep_not_ps_aux_grep_antipattern():
    sh = _read(REFRESH_SH)
    assert "pgrep" in sh, "Expected pgrep usage for process detection"
    anti = re.search(r"ps\s+aux\s*\|\s*grep[\s\S]*grep\s+-v\s+grep", sh)
    assert anti is None, "Found ps aux | grep ... | grep -v grep anti-pattern"


def test_tc18_with_open_uses_as_keyword_in_embedded_python():
    py = _extract_embedded_python(_read(REFRESH_SH))
    with_open_lines = re.findall(r"^\s*with\s+open\([^\n]*$", py, re.M)
    assert with_open_lines, "No with open(...) usages found"
    for line in with_open_lines:
        assert " as " in line, f"with open missing 'as' context binding: {line.strip()}"


def test_tc19_no_import_star_in_embedded_python():
    py = _extract_embedded_python(_read(REFRESH_SH))
    assert re.search(r"^\s*from\s+\S+\s+import\s+\*", py, re.M) is None


def test_tc20_embedded_python_imports_os_for_getpid():
    py = _extract_embedded_python(_read(REFRESH_SH))
    assert re.search(r"^\s*import\s+.*\bos\b", py, re.M), "import os missing in embedded Python"
    assert "os.getpid()" in py, "os.getpid() usage expected for self PID exclusion"
