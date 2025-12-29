#!/usr/bin/env python3
import json
import math
import os
import statistics
from dataclasses import dataclass
from typing import Any, Dict, List, Tuple


IN_PATH = "artifacts/phase5_6_divergence_reject_samples.json"
OUT_TXT = "artifacts/phase5_7_divergence_rootcause_report.txt"
OUT_JSON = "artifacts/phase5_7_divergence_rootcause_report.json"


def _q(sorted_vals: List[float], q: float) -> float:
    if not sorted_vals:
        return 0.0
    if q <= 0:
        return float(sorted_vals[0])
    if q >= 1:
        return float(sorted_vals[-1])
    pos = q * (len(sorted_vals) - 1)
    lo = int(math.floor(pos))
    hi = int(math.ceil(pos))
    if lo == hi:
        return float(sorted_vals[lo])
    frac = pos - lo
    return float(sorted_vals[lo] * (1 - frac) + sorted_vals[hi] * frac)


def _safe_div(a: float, b: float) -> float:
    return a / max(b, 1e-12)


def classify(sample: Dict[str, Any]) -> Tuple[str, Dict[str, float]]:
    age_a = int(sample.get("age_a_ms") or 0)
    age_b = int(sample.get("age_b_ms") or 0)
    age_gap = abs(age_a - age_b)
    a = float(sample.get("source_a_price_norm") or 0.0)
    b = float(sample.get("source_b_price_norm") or 0.0)
    mx = max(a, b)
    mn = min(a, b)
    scale_ratio = _safe_div(mx, mn)
    dev = int(sample.get("deviation_bps") or 0)

    metrics = {
        "age_gap_ms": float(age_gap),
        "scale_ratio": float(scale_ratio),
        "deviation_bps": float(dev),
        "age_a_ms": float(age_a),
        "age_b_ms": float(age_b),
    }

    # Priority classification
    if age_gap >= 10_000 or age_a >= 30_000 or age_b >= 30_000:
        return "TIME_MISMATCH", metrics
    if (
        scale_ratio >= 1000
        or a < 1e-9
        or a > 1e9
        or b < 1e-9
        or b > 1e9
    ):
        return "SEMANTIC_MISMATCH", metrics
    # C is a statistical conclusion; per-sample label just tracks "CANDIDATE_C".
    if 100 <= dev <= 500:
        return "THRESHOLD_TOO_STRICT_OR_ENV", metrics
    return "UNKNOWN", metrics


def _top(samples: List[Dict[str, Any]], key: str, n: int = 5) -> List[Dict[str, Any]]:
    return sorted(samples, key=lambda s: float(s.get(key) or 0.0), reverse=True)[:n]


def main() -> int:
    if not os.path.exists(IN_PATH):
        raise SystemExit(f"missing {IN_PATH}")
    data = json.load(open(IN_PATH, "r"))
    run_id = data.get("run_id") or ""
    if run_id == "smoke-run":
        raise SystemExit(f"{IN_PATH} is smoke-run; run 2h retest first")

    samples = data.get("samples_top_by_deviation") or []
    if not isinstance(samples, list):
        raise SystemExit("invalid samples_top_by_deviation")

    classified: List[Dict[str, Any]] = []
    count_by: Dict[str, int] = {}
    devs: List[float] = []
    gaps: List[float] = []
    ratios: List[float] = []

    for s in samples:
        cls, m = classify(s)
        count_by[cls] = count_by.get(cls, 0) + 1
        s2 = dict(s)
        s2["class"] = cls
        s2["age_gap_ms"] = int(m["age_gap_ms"])
        s2["scale_ratio"] = m["scale_ratio"]
        classified.append(s2)
        devs.append(m["deviation_bps"])
        gaps.append(m["age_gap_ms"])
        ratios.append(m["scale_ratio"])

    dominant = max(count_by.items(), key=lambda kv: kv[1])[0] if count_by else "UNKNOWN"
    # Tie-breaker: A > B > C
    if count_by:
        best_n = max(count_by.values())
        tied = {k for k, v in count_by.items() if v == best_n}
        for pref in ["TIME_MISMATCH", "SEMANTIC_MISMATCH", "THRESHOLD_TOO_STRICT_OR_ENV", "UNKNOWN"]:
            if pref in tied:
                dominant = pref
                break

    recommended_path = {"TIME_MISMATCH": "A", "SEMANTIC_MISMATCH": "B", "THRESHOLD_TOO_STRICT_OR_ENV": "C"}.get(dominant, "A")

    devs_s = sorted(devs)
    gaps_s = sorted(gaps)
    ratios_s = sorted(ratios)

    report = {
        "input_path": IN_PATH,
        "run_id": run_id,
        "started_at": data.get("started_at"),
        "ended_at": data.get("ended_at"),
        "threshold_bps": data.get("threshold_bps"),
        "total_samples": len(samples),
        "count_by_class": count_by,
        "dominant_class": dominant,
        "recommended_path": recommended_path,
        "quantiles": {
            "deviation_bps": {"p50": _q(devs_s, 0.5), "p95": _q(devs_s, 0.95), "max": _q(devs_s, 1.0)},
            "age_gap_ms": {"p50": _q(gaps_s, 0.5), "p95": _q(gaps_s, 0.95), "max": _q(gaps_s, 1.0)},
            "scale_ratio": {"p50": _q(ratios_s, 0.5), "p95": _q(ratios_s, 0.95), "max": _q(ratios_s, 1.0)},
        },
        "top5_samples_by_deviation": _top(classified, "deviation_bps", 5),
        "top5_samples_by_age_gap": _top(classified, "age_gap_ms", 5),
    }

    os.makedirs("artifacts", exist_ok=True)
    with open(OUT_JSON, "w") as f:
        json.dump(report, f, indent=2, sort_keys=True)

    lines: List[str] = []
    lines.append("Phase 5.7 divergence root-cause report")
    lines.append(f"input={IN_PATH}")
    lines.append(f"run_id={run_id} started_at={data.get('started_at')} ended_at={data.get('ended_at')}")
    lines.append(f"threshold_bps={data.get('threshold_bps')}")
    lines.append("")
    lines.append(f"total_samples={report['total_samples']}")
    lines.append(f"count_by_class={json.dumps(count_by, sort_keys=True)}")
    lines.append(f"dominant_class={dominant} recommended_path={recommended_path}")
    lines.append("")
    qd = report["quantiles"]["deviation_bps"]
    qg = report["quantiles"]["age_gap_ms"]
    qr = report["quantiles"]["scale_ratio"]
    lines.append(f"deviation_bps p50={qd['p50']:.0f} p95={qd['p95']:.0f} max={qd['max']:.0f}")
    lines.append(f"age_gap_ms p50={qg['p50']:.0f} p95={qg['p95']:.0f} max={qg['max']:.0f}")
    lines.append(f"scale_ratio p50={qr['p50']:.2f} p95={qr['p95']:.2f} max={qr['max']:.2f}")
    lines.append("")

    def brief(s: Dict[str, Any]) -> str:
        return (
            f"class={s.get('class')} dev_bps={s.get('deviation_bps')} "
            f"age_gap_ms={s.get('age_gap_ms')} scale_ratio={s.get('scale_ratio'):.2f} "
            f"pool={s.get('pool_id')} chain={s.get('chain_id')} key={s.get('key')}"
        )

    lines.append("top5_by_deviation:")
    for s in report["top5_samples_by_deviation"]:
        lines.append("  " + brief(s))
    lines.append("")
    lines.append("top5_by_age_gap:")
    for s in report["top5_samples_by_age_gap"]:
        lines.append("  " + brief(s))
    lines.append("")

    with open(OUT_TXT, "w") as f:
        f.write("\n".join(lines) + "\n")

    print(OUT_TXT)
    print(OUT_JSON)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

