#!/usr/bin/env python3
"""
SABnzbd post-process script for hasharr.

Flow:
1) Scan completed download directory for video files.
2) Exclude files containing ".sample" and (when exactly two videos exist) files ending ".1.<ext>".
3) For each file:
   - Exact sweep: maxDistance=0, maxTimeDelta=1
   - If exact matches exist:
       - Compare source against exact-match cards by largest resolution, duration, fps (fps capped at 30).
       - If source has no advantages (L/D/F), delete source file.
       - Else prepend reason tag, e.g. [LD]Filename.ext
   - If no exact matches:
       - Optimistic sweep: maxDistance=8, maxTimeDelta=min(15, duration*0.02)
       - If any match found, prepend [P]
4) If every scanned video got deleted, delete the job directory and exit 1.
5) If only potentials were found (no exact outcomes), exit 2.
6) Otherwise exit 0.
"""

from __future__ import annotations

import json
import os
import re
import shutil
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, List, Optional, Tuple
from urllib import error, request

# Defaults can be overridden in downloaded script builds.
DEFAULT_STASH_INDEX = globals().get("DEFAULT_STASH_INDEX", -1)
DEFAULT_MAX_TIME_DELTA = globals().get("DEFAULT_MAX_TIME_DELTA", 1.0)
DEFAULT_MAX_DISTANCE = globals().get("DEFAULT_MAX_DISTANCE", 0)
DEFAULT_HASHARR_URL = globals().get("DEFAULT_HASHARR_URL", "http://hasharr:9995")


VIDEO_EXTS = {
    ".3gp",
    ".asf",
    ".avi",
    ".flv",
    ".m2ts",
    ".m4v",
    ".mkv",
    ".mov",
    ".mp4",
    ".mpeg",
    ".mpg",
    ".mts",
    ".ts",
    ".vob",
    ".webm",
    ".wmv",
}


def log(msg: str) -> None:
    print(f"[hasharr] {msg}", flush=True)


def get_job_dir(argv: List[str]) -> Optional[Path]:
    # SAB passes final dir as arg1; env fallback exists too.
    raw = argv[1] if len(argv) > 1 else os.environ.get("SAB_COMPLETE_DIR", "")
    raw = (raw or "").strip()
    if not raw:
        return None
    return Path(raw)


def is_video(path: Path) -> bool:
    return path.is_file() and path.suffix.lower() in VIDEO_EXTS


def scan_videos(root: Path) -> List[Path]:
    files = [p for p in root.rglob("*") if is_video(p)]
    files = [p for p in files if ".sample" not in p.name.lower()]

    # Special-case: if exactly 2 videos, ignore a ".1.<ext>" split part.
    if len(files) == 2:
        dot_one = re.compile(r"\.1\.[^.]+$", re.IGNORECASE)
        files = [p for p in files if not dot_one.search(p.name)]

    return sorted(files)


def api_post_json(base_url: str, path: str, payload: Dict) -> Dict:
    url = base_url.rstrip("/") + path
    data = json.dumps(payload).encode("utf-8")
    req = request.Request(
        url,
        data=data,
        method="POST",
        headers={"Content-Type": "application/json"},
    )
    try:
        with request.urlopen(req, timeout=120) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {exc.code} from {url}: {body}") from exc
    except error.URLError as exc:
        raise RuntimeError(f"Failed to call {url}: {exc}") from exc


def fetch_card(base_url: str, endpoint_url: str, scene_id: str) -> Dict:
    return api_post_json(
        base_url,
        "/v1/scene-card",
        {"endpointUrl": endpoint_url, "sceneId": str(scene_id)},
    )


def normalize_fps(v: object) -> float:
    try:
        f = float(v)
    except (TypeError, ValueError):
        return 0.0
    if f <= 0:
        return 0.0
    return min(f, 30.0)


def safe_num(v: object) -> float:
    try:
        return float(v)
    except (TypeError, ValueError):
        return 0.0


def prefix_name(path: Path, prefix: str) -> Path:
    if path.name.startswith(prefix):
        return path
    # Avoid stacking tags repeatedly.
    if re.match(r"^\[[A-Z]+\]", path.name):
        stripped = re.sub(r"^\[[A-Z]+\]", "", path.name)
        new_name = f"{prefix}{stripped}"
    else:
        new_name = f"{prefix}{path.name}"
    return path.with_name(new_name)


def rename_with_prefix(path: Path, prefix: str) -> Path:
    target = prefix_name(path, prefix)
    if target == path:
        return path
    if target.exists():
        stem = target.stem
        suffix = target.suffix
        i = 2
        while True:
            alt = target.with_name(f"{stem}.{i}{suffix}")
            if not alt.exists():
                target = alt
                break
            i += 1
    path.rename(target)
    return target


@dataclass
class Outcome:
    deleted: int = 0
    tagged_exact: int = 0
    tagged_potential: int = 0
    untouched: int = 0


def flatten_matches(lookup_result: Dict) -> List[Tuple[str, Dict]]:
    rows: List[Tuple[str, Dict]] = []
    for lookup in lookup_result.get("lookups", []) or []:
        endpoint_url = str(lookup.get("endpointUrl", "")).strip()
        if not endpoint_url:
            continue
        matches = lookup.get("matches", {}) or {}
        for key in ("exactMatches", "partialMatches"):
            for row in matches.get(key, []) or []:
                if row and row.get("id"):
                    rows.append((endpoint_url, row))
    return rows


def process_file(path: Path, base_url: str) -> Tuple[str, Optional[Path]]:
    log(f"Processing: {path}")
    exact_payload: Dict[str, object] = {
        "filePath": str(path),
        "maxTimeDelta": float(DEFAULT_MAX_TIME_DELTA),
        "maxDistance": int(DEFAULT_MAX_DISTANCE),
    }
    if int(DEFAULT_STASH_INDEX) != -1:
        exact_payload["stashIndex"] = int(DEFAULT_STASH_INDEX)
    exact = api_post_json(base_url, "/v1/phash-match", exact_payload)
    source_hash = exact.get("hash", {}) or {}
    source_y = safe_num(source_hash.get("resolution_y"))
    source_duration = safe_num(source_hash.get("duration"))
    source_fps = normalize_fps(source_hash.get("frame_rate"))

    exact_rows: List[Tuple[str, Dict]] = []
    for lookup in exact.get("lookups", []) or []:
        endpoint_url = str(lookup.get("endpointUrl", "")).strip()
        if not endpoint_url:
            continue
        for row in (lookup.get("matches", {}) or {}).get("exactMatches", []) or []:
            if row and row.get("id"):
                exact_rows.append((endpoint_url, row))

    if exact_rows:
        max_y = 0.0
        max_dur = 0.0
        max_fps = 0.0
        matched_metrics: List[Tuple[float, float, float]] = []
        for endpoint_url, row in exact_rows:
            try:
                card = fetch_card(base_url, endpoint_url, str(row.get("id")))
            except Exception as exc:
                log(f"  warn: failed scene-card lookup for {row.get('id')}: {exc}")
                continue
            y = safe_num(card.get("resolutionY"))
            d = safe_num(card.get("duration"))
            f = normalize_fps(card.get("frameRate"))
            matched_metrics.append((y, d, f))
            max_y = max(max_y, y)
            max_dur = max(max_dur, d)
            max_fps = max(max_fps, f)

        reasons: List[str] = []
        duration_delta = source_duration - max_dur

        any_lower_y = any(source_y > y for (y, _, _) in matched_metrics)
        any_meaningfully_shorter = any((source_duration - d) > 1 for (_, d, _) in matched_metrics)
        any_lower_fps = any(source_fps > f for (_, _, f) in matched_metrics)

        if source_y >= max_y and any_lower_y:
            log(f"  tag reason L: source resolution_y {source_y:.0f} is top-tier and exceeds lower exact matches")
            reasons.append("L")
        if source_duration >= (max_dur - 1) and any_meaningfully_shorter:
            log("  tag reason D: source duration is top-tier and exceeds at least one exact match by >1.00s")
            reasons.append("D")
        if source_fps >= max_fps and any_lower_fps:
            log("  tag reason F: source fps is top-tier and exceeds lower exact matches (fps normalized, cap=30)")
            reasons.append("F")

        has_advantage = bool(reasons)

        if not has_advantage:
            path.unlink(missing_ok=True)
            log("  exact matches found; file is not better -> deleted")
            return "deleted", None

        if reasons:
            log(
                "  exact comparison: "
                f"resY src={source_y:.0f} vs max={max_y:.0f}; "
                f"dur src={source_duration:.2f} vs max={max_dur:.2f} (delta={duration_delta:.2f}s, threshold>1s); "
                f"fps src={source_fps:.2f} vs max={max_fps:.2f}"
            )
            tagged = rename_with_prefix(path, f"[{''.join(reasons)}]")
            log(f"  exact matches found; better by {''.join(reasons)} -> tagged as {tagged.name}")
            return "tagged_exact", tagged

        log("  exact matches found; kept (L/D/F advantage)")
        return "untouched", path

    optimistic_delta = min(15.0, source_duration * 0.02 if source_duration > 0 else 0.0)
    optimistic = api_post_json(
        base_url,
        "/v1/phash-match",
        {"filePath": str(path), "maxTimeDelta": optimistic_delta, "maxDistance": 8},
    )
    optimistic_rows = flatten_matches(optimistic)
    if optimistic_rows:
        tagged = rename_with_prefix(path, "[P]")
        log(f"  no exact matches; potential matches found -> tagged as {tagged.name}")
        return "tagged_potential", tagged

    log("  no exact/potential matches -> left unchanged")
    return "untouched", path

def clean_exit():
    print(f"Completed", flush=True)
    sys.exit(0)


def main(argv: List[str]) -> int:
    job_dir = get_job_dir(argv)
    if not job_dir:
        log("No SAB job directory argument received.")
        clean_exit()
    if not job_dir.exists():
        log(f"Job directory does not exist: {job_dir}")
        clean_exit()

    base_url = os.environ.get("HASHARR_URL", str(DEFAULT_HASHARR_URL)).strip()
    log(f"Using hasharr endpoint: {base_url}")
    log(f"Scanning directory: {job_dir}")

    videos = scan_videos(job_dir)
    if not videos:
        log("No eligible video files found.")
        clean_exit()

    outcome = Outcome()
    for video in videos:
        try:
            result, _ = process_file(video, base_url)
        except Exception as exc:
            log(f"  error while processing {video.name}: {exc}")
            outcome.untouched += 1
            continue

        if result == "deleted":
            outcome.deleted += 1
        elif result == "tagged_exact":
            outcome.tagged_exact += 1
        elif result == "tagged_potential":
            outcome.tagged_potential += 1
        else:
            outcome.untouched += 1

    total = len(videos)
    

    if outcome.deleted == total:
        try:
            log(
                "Summary: "
                f"total={total}, deleted={outcome.deleted}, "
                f"Deleting job directory because all eligible videos were deleted: {job_dir}"
                
            )
            shutil.rmtree(job_dir)
            log("Empty folder deleted successfully")
        except Exception as exc:
            log(f"Failed deleting job directory {job_dir}: {exc}")
            return 1
        return 0

    if outcome.tagged_potential > 0 and outcome.deleted == 0 and outcome.tagged_exact == 0:
        log(
            "Summary: "
            f"total={total}, deleted={outcome.deleted}, "
            f"tagged_exact={outcome.tagged_exact}, tagged_potential={outcome.tagged_potential}, "
            f"untouched={outcome.untouched}"
        )
        return 0

    clean_exit()


if __name__ == "__main__":
    sys.exit(main(sys.argv))
