#!/usr/bin/env python3
"""
Scan all OpenClaw session JSONL files and compute real token + cost stats.
Outputs structured JSON for injection into data.json.
"""
import json, glob, os, sys
from datetime import datetime, timezone, timedelta
from collections import defaultdict

OPENCLAW_PATH = os.environ.get("OPENCLAW_HOME", os.path.expanduser("~/.openclaw"))

# Anthropic pricing per 1M tokens (as of 2025/2026)
PRICING = {
    "claude-opus-4-5":     {"input": 15.0,  "output": 75.0,  "cacheRead": 1.5,  "cacheWrite": 18.75},
    "claude-opus-4":       {"input": 15.0,  "output": 75.0,  "cacheRead": 1.5,  "cacheWrite": 18.75},
    "claude-sonnet-4-6":   {"input": 3.0,   "output": 15.0,  "cacheRead": 0.3,  "cacheWrite": 3.75},
    "claude-sonnet-4-5":   {"input": 3.0,   "output": 15.0,  "cacheRead": 0.3,  "cacheWrite": 3.75},
    "claude-sonnet-4":     {"input": 3.0,   "output": 15.0,  "cacheRead": 0.3,  "cacheWrite": 3.75},
    "claude-haiku-4-5":    {"input": 0.8,   "output": 4.0,   "cacheRead": 0.08, "cacheWrite": 1.0},
    "claude-haiku-4":      {"input": 0.8,   "output": 4.0,   "cacheRead": 0.08, "cacheWrite": 1.0},
}
DEFAULT_PRICING = {"input": 3.0, "output": 15.0, "cacheRead": 0.3, "cacheWrite": 3.75}

def get_price(model, kind):
    p = PRICING.get(model, DEFAULT_PRICING)
    return p.get(kind, 0)

now_utc = datetime.now(timezone.utc)
today_start = now_utc.replace(hour=0, minute=0, second=0, microsecond=0)
week_start  = today_start - timedelta(days=6)
month_start = today_start - timedelta(days=29)

def make_bucket():
    return {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0,
            "totalTokens": 0, "cost": 0.0}

buckets = {"today": make_bucket(), "7d": make_bucket(), "30d": make_bucket(), "allTime": make_bucket()}
by_model_today   = defaultdict(make_bucket)
by_model_7d      = defaultdict(make_bucket)
by_model_alltime = defaultdict(make_bucket)

agents_dir = os.path.join(OPENCLAW_PATH, "agents")
session_files = glob.glob(os.path.join(agents_dir, "**", "sessions", "*.jsonl"), recursive=True)
# also try flat structure
session_files += glob.glob(os.path.join(OPENCLAW_PATH, "agents", "*", "*.jsonl"))

processed = 0

for fpath in session_files:
    if ".deleted." in fpath or ".lock" in fpath:
        continue
    try:
        with open(fpath, "r", encoding="utf-8") as f:
            for raw in f:
                raw = raw.strip()
                if not raw:
                    continue
                try:
                    d = json.loads(raw)
                except json.JSONDecodeError:
                    continue

                msg = d.get("message", {})
                if msg.get("role") != "assistant":
                    continue

                usage = msg.get("usage")
                if not usage:
                    continue

                ts_str = d.get("timestamp") or msg.get("timestamp")
                if not ts_str:
                    continue
                try:
                    ts = datetime.fromisoformat(ts_str.replace("Z", "+00:00"))
                except Exception:
                    continue

                model = msg.get("model", "claude-sonnet-4-6")

                inp   = usage.get("input", 0)
                out   = usage.get("output", 0)
                cr    = usage.get("cacheRead", 0)
                cw    = usage.get("cacheWrite", 0)
                total = inp + out + cr + cw

                # Cost: use embedded cost if available, else compute
                cost_obj = usage.get("cost", {})
                if isinstance(cost_obj, dict) and cost_obj.get("total"):
                    cost = cost_obj["total"]
                else:
                    cost = (
                        inp * get_price(model, "input") / 1_000_000
                        + out * get_price(model, "output") / 1_000_000
                        + cr  * get_price(model, "cacheRead") / 1_000_000
                        + cw  * get_price(model, "cacheWrite") / 1_000_000
                    )

                def add_to(bucket, model_bucket=None):
                    bucket["input"]       += inp
                    bucket["output"]      += out
                    bucket["cacheRead"]   += cr
                    bucket["cacheWrite"]  += cw
                    bucket["totalTokens"] += total
                    bucket["cost"]        += cost
                    if model_bucket is not None:
                        model_bucket["input"]       += inp
                        model_bucket["output"]      += out
                        model_bucket["cacheRead"]   += cr
                        model_bucket["cacheWrite"]  += cw
                        model_bucket["totalTokens"] += total
                        model_bucket["cost"]        += cost

                add_to(buckets["allTime"], by_model_alltime[model])
                if ts >= month_start:
                    add_to(buckets["30d"])
                if ts >= week_start:
                    add_to(buckets["7d"], by_model_7d[model])
                if ts >= today_start:
                    add_to(buckets["today"], by_model_today[model])

                processed += 1

    except Exception:
        continue

def fmt_bucket(b):
    return {
        "inputTokens":      b["input"],
        "outputTokens":     b["output"],
        "cacheReadTokens":  b["cacheRead"],
        "cacheWriteTokens": b["cacheWrite"],
        "totalTokens":      b["totalTokens"],
        "cost":             round(b["cost"], 6),
    }

def fmt_model_breakdown(d):
    rows = []
    for model, b in sorted(d.items(), key=lambda x: -x[1]["cost"]):
        rows.append({"model": model, **fmt_bucket(b)})
    return rows

result = {
    "generatedAt": now_utc.isoformat(),
    "today":   fmt_bucket(buckets["today"]),
    "week7d":  fmt_bucket(buckets["7d"]),
    "month30d": fmt_bucket(buckets["30d"]),
    "allTime": fmt_bucket(buckets["allTime"]),
    "byModelToday":   fmt_model_breakdown(by_model_today),
    "byModel7d":      fmt_model_breakdown(by_model_7d),
    "byModelAllTime": fmt_model_breakdown(by_model_alltime),
    "sessionsScanned": processed,
}

print(json.dumps(result, indent=2))
