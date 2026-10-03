"""Local 20-upstream Seedance scheduler integration laboratory.

Requires the retained, inspected stress fixture library and cached Docker images
described in docs/testing/video-scheduler-mock-lab.md. Never targets a supplied
gateway URL. Only HTTP management APIs mutate gateway data; SQL is read-only.
Private credentials and full evidence stay in the ignored fixture run directory.
"""

from __future__ import annotations

import argparse
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from copy import deepcopy
from decimal import Decimal
import json
import os
from pathlib import Path
import re
import sys
import time

import requests

REPO = Path(__file__).resolve().parents[2]
LIBRARY = REPO / ".scratch/unified-video-model-plan/stress"
sys.path.insert(0, str(LIBRARY))
from fixture import Fixture  # noqa: E402
from run_stress import Runner, sql_string, write_json  # noqa: E402

MODEL = "seedance-2.0"
TIERS = ("480p", "720p", "1080p", "4k")
SIZES = dict(zip(TIERS, ("854x480", "1280x720", "1920x1080", "3840x2160")))
SALES = dict(zip(TIERS, (0.56, 1, 3, 5)))
SECONDS = list(range(4, 16))
# Each row is a different upstream, not another credential on one channel.
# Prices are illustrative procurement USD, not provider price claims.
ROWS = [
    (1, "seedance-hjmie", "videos-mini", "per_video", {"480p": .8}),
    (1, "pidoi", "jiuyue111", "per_video", {"720p": 1.8}),
    (1, "meaicc", "sd-2-c1", "per_video", {"720p": 1.85}),
    (1, "seedance-hjmie", "videos-mini", "per_video", {"4k": 9}),
    (2, "pidoi", "tejiasd-mini-720p", "per_video", {"480p": .9, "720p": 1.6}),
    (2, "seedance-hjmie", "videos-mini", "per_video", dict(zip(TIERS, (.95, 1.7, 4.7, 8.5)))),
    (2, "seedance-hjmie", "videos-mini", "per_video", {"720p": 1.9, "1080p": 4.9}),
    (2, "paipu", "lec-gt-seedance-2-0-full", "per_video", {"480p": .85, "720p": 1.75, "1080p": 5.2}),
    (3, "pidoi", "seedace-2.0-480p", "per_second", {"480p": .1}),
    (3, "pidoi", "Bt-sd2.0-720p", "per_second", {"720p": .19}),
    (3, "megabyai", "videos-mini", "per_second", {"1080p": .5}),
    (3, "megabyai", "videos-mini", "per_second", {"4k": .9}),
    (4, "seedance-hjmie", "videos-mini", "per_second", dict(zip(TIERS, (.11, .20, .55, .95)))),
    (4, "megabyai", "videos-mini", "per_second", dict(zip(TIERS, (.13, .18, .48, .85)))),
    (4, "paipu", "lec-gt-seedance-2-0-full", "per_second", {"480p": .105, "720p": .21, "1080p": .52}),
    (4, "seedance-hjmie", "videos-mini", "per_second", dict(zip(TIERS, (.14, .22, .62, 1.1)))),
    (5, "seedance-hjmie", "videos-mini", "per_second", dict(zip(TIERS, (.08, .14, .40, .75)))),
    (5, "megabyai", "videos-mini", "per_second", dict(zip(TIERS, (.09, .15, .42, .76)))),
    (5, "seedance-hjmie", "videos-mini", "per_second", dict(zip(TIERS, (.12, .16, .45, .79)))),
    (5, "megabyai", "videos-mini", "per_second", dict(zip(TIERS, (.11, .17, .43, .78)))),
]


def profiles():
    result = []
    extras = {17: (.08, .12, .3, .5), 18: (.004, .008, .02, .04),
              19: (.03, .04, .08, .14), 20: (.04, .06, .1, .2)}
    for number, (category, plugin, model, mode, prices) in enumerate(ROWS, 1):
        allowed = {tier: SECONDS.copy() for tier in prices}
        if number == 2:
            allowed["720p"] = [5, 10, 15]
        if number == 5:
            allowed["720p"] = list(range(4, 13))
        if number == 16:
            allowed["4k"] = list(range(10, 16))
        references = {kind: {"*": {"mode": "included"}} for kind in ("image", "video", "audio")}
        if number in (2, 5):
            references["video"] = {"*": {"mode": "unsupported"}}
        if number in extras:
            references["video"] = {tier: {"mode": "per_output_second" if number == 19 else "per_input_second", "value": amount}
                                   for tier, amount in zip(TIERS, extras[number])}
        cost = {"mode": mode, "prices": prices, "allowed_seconds_by_resolution": allowed, "references": references}
        result.append({"number": number, "category": category, "plugin": plugin, "upstream_model": model, "cost": cost})
    return result


PROFILES = profiles()


def expected_candidates(case, numbers=range(1, 21)):
    """Independent Decimal cost oracle; never reads the scheduler's chosen cost."""
    tier, seconds = case["resolution"], case["seconds"]
    costs = {}
    for number in numbers:
        cost = PROFILES[number - 1]["cost"]
        if seconds not in cost["allowed_seconds_by_resolution"].get(tier, []):
            continue
        amount = Decimal(str(cost["prices"][tier])) * (seconds if cost["mode"] == "per_second" else 1)
        if case.get("videos"):
            rule = cost["references"]["video"].get(tier, cost["references"]["video"].get("*"))
            if not rule or rule["mode"] == "unsupported":
                continue
            if rule["mode"] == "per_input_second":
                if case.get("input_seconds") is None:
                    continue
                amount += Decimal(str(rule["value"])) * Decimal(str(case["input_seconds"]))
            if rule["mode"] == "per_output_second":
                amount += Decimal(str(rule["value"])) * seconds
        sale = Decimal(str(SALES[tier])) * seconds * Decimal(str(case.get("ratio", 1)))
        if amount <= sale * Decimal(".9"):
            costs[number] = amount
    best = min(costs.values()) if costs else None
    return {"numbers": [number for number, cost in costs.items() if cost == best],
            "cost": float(best) if best is not None else None,
            "quotes": {number: float(cost) for number, cost in costs.items()}}


class SeedanceRunner(Runner):
    def __init__(self, fixture, args):
        super().__init__(fixture, args)
        if not args.resume:
            self.report = {"label": args.label, "model": MODEL, "sales_usd_per_second": SALES,
                           "profiles": PROFILES, "phases": [], "complete": False}
        self.report.update({"model": MODEL, "sales_usd_per_second": SALES, "profiles": PROFILES})
        self.report.pop("sale_usd", None)
        self.report.pop("quota", None)

    def bootstrap(self):
        if self.args.resume and (self.lab.folder / "relay-tokens.private.json").exists():
            super().bootstrap()
            return
        creds = self.lab.credentials
        self.lab.api("POST", "/api/setup", {"username": creds["username"], "password": creds["password"],
                    "confirmPassword": creds["password"], "SelfUseModeEnabled": True, "DemoSiteEnabled": False})
        self.lab.login()
        self.user_id = self.lab.rows("SELECT id FROM users ORDER BY id LIMIT 1")[0]["id"]
        self.lab.api("POST", "/api/user/manage", {"id": self.user_id, "action": "add_quota", "mode": "add", "value": 500000000000})
        options = {item["key"]: item["value"] for item in self.lab.api("GET", "/api/option/")}
        groups = ["seedance-all", "seedance-discount"] + [f"seedance-{n:02d}" for n in range(1, 21)]
        ratios, usable = json.loads(options["GroupRatio"]), json.loads(options["UserUsableGroups"])
        ratios.update({g: .1 if g == "seedance-discount" else 1 for g in groups})
        usable.update({g: g for g in groups})
        self.lab.option("GroupRatio", ratios)
        self.lab.option("UserUsableGroups", usable)
        self.lab.option("RetryTimes", 3)
        self.lab.option("fetch_setting.allow_private_ip", True)
        self.lab.sched(mode="off", selection_policy="stability_cost_v2", min_gen_rate=.8, min_overall_rate=.6,
            min_margin_rate=.1, min_samples=20, window_seconds=10, qualification_ttl_seconds=3600,
            validation_period_seconds=1800, stability_tolerance=.01, explore_share=1,
            explore_max_in_flight=4, probe_ratio=0, probe_cooldown_sec=2,
            probe_max_in_flight=1, audit_enabled=True, models=[MODEL])
        self.sale(disabled=True)
        for profile in PROFILES:
            number = profile["number"]
            self.lab.api("POST", "/api/channel/", {"mode": "single", "channel": {
                "type": 61, "name": f"seedance-{number:02d}-type{profile['category']}-{profile['plugin']}", "key": "local-fixture-only",
                "status": 1, "base_url": self.lab.channel_base(number-1), "models": MODEL,
                "group": f"seedance-all,seedance-discount,seedance-{number:02d}", "weight": 1, "priority": 0, "auto_ban": 0,
                "model_mapping": json.dumps({MODEL: profile["upstream_model"]}), "setting": json.dumps({"task_plugin_key": profile["plugin"]}),
                "settings": json.dumps({"disable_task_polling_sleep": True, "video_scheduling": {
                    "quality": .9, "capacity": 0, "models": {MODEL: profile["cost"]}}})}})
        self.channels = self.lab.rows("SELECT id,name,models,model_mapping,setting,settings,status FROM channels ORDER BY id")
        assert len(self.channels) == 20, "Expected exactly twenty owned channels"
        for group in groups:
            self.lab.api("POST", "/api/token/", {"name": group, "expired_time": -1,
                "unlimited_quota": False, "remain_quota": 500000000000, "group": group})
            row = self.lab.rows("SELECT id,key FROM tokens WHERE name=" + sql_string(group))[0]
            self.tokens[group] = {"id": row["id"], "relay_key": row["key"]}
        write_json(self.lab.folder / "relay-tokens.private.json", self.tokens)
        self.sale()
        self.lab.sched(mode="on")
        deadline = time.monotonic() + 90
        while self.lab._run(self.lab.compose + ["exec", "-T", "cache", "redis-cli", "exists", "video_sched:capacity_owners_ready"]).stdout.strip() != "1":
            if time.monotonic() >= deadline:
                raise RuntimeError("Gateway startup calibration did not become ready")
            time.sleep(2)
        self.save()

    def events(self, cursors):
        return [{**event, "plugin": PROFILES[event["number"]-1]["plugin"]} for event in super().events(cursors)]

    def completed(self, label):
        previous = next((phase for phase in self.report["phases"] if phase["label"] == label), None)
        if previous and any(not check["passed"] for check in previous["checks"]):
            raise RuntimeError(f"{label} contains failed checks; diagnose first, then use a new --phase-prefix to preserve its evidence")
        return bool(previous and previous.get("finalized"))

    def sale(self, disabled=False, multiplier=1):
        self.lab.option("billing_setting.video_sales", {MODEL: {"disabled": disabled, "resolutions": {
            tier: {"usd_per_second": rate * multiplier, "seconds": SECONDS} for tier, rate in SALES.items()}}})

    def configure(self, number, **changes):
        channel = self.lab.api("GET", f"/api/channel/{self.channels[number-1]['id']}")
        channel.pop("status", None)
        channel.pop("video_health", None)
        settings = json.loads(channel.get("settings") or "{}")
        config = settings["video_scheduling"]
        for key in ("quality", "capacity", "capacity_group"):
            if key in changes:
                config[key] = changes.pop(key)
        if "cost" in changes:
            config["models"][MODEL] = changes.pop("cost")
        channel["settings"] = json.dumps(settings)
        channel.update(changes)
        self.lab.api("PUT", "/api/channel/", channel)

    def post_case(self, case):
        token = self.tokens[case.get("group", "seedance-all")]
        body = {"model": MODEL, "prompt": case["prompt"], "seconds": str(case["seconds"]),
                "size": case.get("size", SIZES.get(case["resolution"], "1280x720")),
                "resolution": case["resolution"]}
        if case.get("videos"):
            body.update({"videos": case["videos"], "images": ["https://inputmedia:8443/reference.png"]})
        body.update(case.get("body", {}))
        started = time.perf_counter()
        record = {**case, "token_id": token["id"], "model": MODEL}
        try:
            if case.get("multipart"):
                files = {key: (None, json.dumps(value) if isinstance(value, list) else str(value)) for key, value in body.items()}
                response = requests.post(self.lab.base + "/v1/videos", headers={"Authorization": "Bearer " + token["relay_key"]}, files=files, timeout=45)
            else:
                response = requests.post(self.lab.base + "/v1/videos", headers={"Authorization": "Bearer " + token["relay_key"]}, json=body, timeout=45)
            record.update({"status": response.status_code, "data": response.json()})
        except (requests.RequestException, ValueError) as error:
            record.update({"status": 0, "error": self.lab._redact(str(error))})
        record["latency_ms"] = (time.perf_counter() - started) * 1000
        return record

    def phase(self, label, cases, concurrency=8, after_submit=None):
        label = getattr(self.args, "phase_prefix", "") + label
        if self.completed(label):
            return next(p for p in self.report["phases"] if p["label"] == label)
        self.drain()
        before, cursors = self.totals(), self.cursors()
        self.begin_phase(label, before, cursors)
        self.report.update({"complete": False, "all_checks_passed": False})
        self.save()
        work = [{**case, "prompt": f"{label}:{index}"} for index, case in enumerate(cases)]
        with ThreadPoolExecutor(max_workers=concurrency) as pool:
            records = list(pool.map(self.post_case, work))
        write_json(self.results / f"{label}-requests.json", records)
        if after_submit:
            after_submit()
        drained = self.drain(210)
        tasks = self.lab.rows(f"SELECT id,task_id,platform,channel_id,status,quota,fail_reason,video_health_attribution,(private_data::json->>'token_id')::bigint token_id,properties::json properties,private_data::json->'billing_context'->'tiered_snapshot' sales_snapshot,private_data::json->'scheduling_summary' scheduling FROM tasks WHERE id>{before['task_id']} ORDER BY id")
        total = sum(task["quota"] for task in tasks)
        deadline = time.monotonic() + 25
        while True:
            after = self.totals()
            money = {"wallet": before["user"]["quota"] - after["user"]["quota"],
                     "user_used": after["user"]["used_quota"] - before["user"]["used_quota"],
                     "token_used": after["token"]["used"] - before["token"]["used"],
                     "token_remaining": before["token"]["remaining"] - after["token"]["remaining"]}
            if all(value == total for value in money.values()) or time.monotonic() >= deadline:
                break
            time.sleep(.5)
        logs = self.lab.rows(f"SELECT id,type,quota,model_name,token_id,other::json other FROM logs WHERE id>{before['log_id']} AND type IN (2,6) ORDER BY id")
        audits = self.lab.rows(f"SELECT request_id,task_pk,task_id,selected_channel,submit_attempts,submit_accepted,submit_rejected,submit_unknown,request_outcome,cost_usd,output_seconds,resolution,reference_video,task_status,terminal_health FROM video_schedule_runs WHERE task_pk>{before['task_id']} ORDER BY id")
        events = self.events(cursors)
        submits = [e for e in events if e["event"] == "submit"]
        accepts = [e for e in events if e["event"] == "accepted"]
        phase = {"label": label, "count": len(records), "terminal": dict(Counter(t["status"] for t in tasks)),
                 "statuses": dict(Counter(r["status"] for r in records)), "checks": [], "drain_seconds": drained,
                 "money": money, "selected_channels": dict(Counter(t["channel_id"] for t in tasks))}
        receipt = {r["data"]["id"]: r for r in records if 200 <= r["status"] < 300 and r.get("data", {}).get("id")}
        self.check(phase, "returned task IDs and persisted tasks correspond one-to-one", len(receipt) == len(tasks) and set(receipt) == {t["task_id"] for t in tasks})
        self.check(phase, "wallet and aggregate token balances match terminal quotas", all(value == total for value in money.values()), money)
        self.check(phase, "consume minus refund logs match terminal quotas", sum(l["quota"] * (1 if l["type"] == 2 else -1) for l in logs) == total)
        self.check(phase, "no duplicate upstream acceptance", all(count == 1 for count in Counter(e["prompt"] for e in accepts).values()))
        expected_tokens = Counter()
        outcomes = []
        for task in tasks:
            case = receipt.get(task["task_id"])
            if not case:
                continue
            tier = "4k" if case["resolution"] == "2160p" else case["resolution"]
            quota = int(Decimal(str(SALES[tier])) * case["seconds"] * Decimal(str(case.get("ratio", 1))) * Decimal(str(case.get("sale_multiplier", 1))) * 500000)
            related = [l for l in logs if l["other"].get("task_id") == task["task_id"]]
            actual_number = next(p["number"] for p, channel in zip(PROFILES, self.channels) if channel["id"] == task["channel_id"])
            outcome = {"prompt": case["prompt"], "seconds": case["seconds"], "resolution": tier, "channel": actual_number,
                       "expected": case.get("expected"), "status": task["status"], "quota": task["quota"], "scheduling": task["scheduling"]}
            outcomes.append(outcome)
            self.check(phase, case["prompt"] + " terminal quota/token/model", task["quota"] == (quota if task["status"] == "SUCCESS" else 0) and task["token_id"] == case["token_id"] and task["properties"].get("origin_model_name") == MODEL, outcome)
            self.check(phase, case["prompt"] + " frozen sale facts", bool(task["sales_snapshot"]) and task["sales_snapshot"]["sales_source"] == "video_request" and task["sales_snapshot"]["usage_facts"] == {"seconds": case["seconds"], "resolution": tier})
            self.check(phase, case["prompt"] + " consume/refund exactly once", Counter(l["type"] for l in related) == ({2: 1} if task["status"] == "SUCCESS" else {2: 1, 6: 1}) and all(l["quota"] == quota and l["token_id"] == case["token_id"] and l["model_name"] == MODEL for l in related))
            if case.get("expected") is not None:
                self.check(phase, case["prompt"] + " independent best-channel oracle", actual_number in case["expected"], outcome)
            if case.get("oracle"):
                audit = [row for row in audits if row["task_id"] == task["task_id"]]
                quote = case["oracle"]["quotes"].get(actual_number)
                self.check(phase, case["prompt"] + " audited procurement cost matches independent quote", len(audit) == 1 and quote is not None and audit[0]["cost_usd"] is not None and abs(audit[0]["cost_usd"]-quote) < 1e-8, {"audit": audit, "quote": quote})
            self.check(phase, case["prompt"] + " terminal outcome", task["status"] == case.get("terminal", "SUCCESS"), outcome)
            expected_tokens[case["token_id"]] += task["quota"]
        for case in records:
            if case.get("reject"):
                self.check(phase, case["prompt"] + " rejected with no upstream submit", 400 <= case["status"] < 600 and not any(e["prompt"] == case["prompt"] for e in submits), case)
            elif case.get("rejected_upstream"):
                self.check(phase, case["prompt"] + " unavailable upstreams retain no task or charge", 400 <= case["status"] < 600 and not case.get("data", {}).get("id") and not any(e["prompt"] == case["prompt"] for e in accepts), case)
            elif case.get("unknown"):
                self.check(phase, case["prompt"] + " uncertain acceptance never retried", case["status"] >= 400 and len([e for e in submits if e["prompt"] == case["prompt"]]) == 1 and len([e for e in accepts if e["prompt"] == case["prompt"]]) == 1, case)
            elif case.get("allow_rejection") and case["status"] >= 400:
                self.check(phase, case["prompt"] + " rejected without a returned task", not case.get("data", {}).get("id"), case)
            else:
                self.check(phase, case["prompt"] + " received task", 200 <= case["status"] < 300 and case.get("data", {}).get("id") in receipt, case)
        for old, new in zip(before["tokens"], after["tokens"]):
            self.check(phase, f"token {old['id']} exact charge", old["id"] == new["id"] and new["used_quota"]-old["used_quota"] == expected_tokens[old["id"]] and old["remain_quota"]-new["remain_quota"] == expected_tokens[old["id"]])
        self.check(phase, "no logs without returned tasks", all(l["other"].get("task_id") in receipt for l in logs))
        outbound_errors = []
        by_prompt = {r["prompt"]: r for r in records}
        for event in submits:
            case = by_prompt[event["prompt"]]
            params = event["body"].get("parameters", event["body"])
            tier = "4k" if case["resolution"] == "2160p" else case["resolution"]
            if (str(params.get("duration", params.get("seconds"))) != str(case["seconds"])
                    or params.get("resolution") != tier or event["body"].get("model") != PROFILES[event["number"]-1]["upstream_model"]):
                outbound_errors.append(event)
        self.check(phase, "mapped upstream model and request dimensions preserved", not outbound_errors, outbound_errors)
        write_json(self.results / f"{label}-evidence.json", {"before": before, "after": after, "records": records, "tasks": tasks, "logs": logs, "audits": audits, "events": events, "outcomes": outcomes})
        self.report["phases"].append(phase)
        self.save_phase(phase, finalized=True)
        failed = [check["name"] for check in phase["checks"] if not check["passed"]]
        print(json.dumps({"phase": label, "count": len(records), "terminal": phase["terminal"], "failed_checks": failed}), flush=True)
        if failed:
            raise AssertionError(f"{label}: {len(failed)} checks failed; evidence preserved")
        return phase

    def qualify(self, suffix="initial", numbers=None):
        numbers = list(numbers or range(1, 21))
        self.pool(numbers)
        self.lab.sched(explore_share=1, probe_ratio=1, explore_max_in_flight=16)
        for number in numbers:
            self.mock(number)
        for iteration in range(1, 17):
            states = self.state(numbers)
            normal = {s["channel_id"] for s in states if s["state"] == "normal" and s["integrity"] == "complete"}
            unready = [n for n in numbers if self.channels[n-1]["id"] not in normal]
            if not unready:
                self.lab.sched(explore_share=0, probe_ratio=0, explore_max_in_flight=4)
                self.report[f"qualification_{suffix}"] = {"rounds": iteration-1, "states": states, "genuine_requests": True}
                self.save()
                return
            cases = []
            for number in unready:
                cost = PROFILES[number-1]["cost"]
                tier = next(iter(cost["prices"]))
                seconds = cost["allowed_seconds_by_resolution"][tier][-1]
                state = next((row for row in states if row["channel_id"] == self.channels[number-1]["id"]), {})
                current = json.loads(state.get("current_json") or "{}")
                batch = min(4 if state.get("state") in ("blocked", "recovering") else 16, max(1, 20-current.get("succeeded", 0)))
                cases += [{"seconds": seconds, "resolution": tier, "group": f"seedance-{number:02d}", "expected": [number], "allow_rejection": True}] * batch
            self.phase(f"qualify-{suffix}-{iteration:02d}", cases, concurrency=32)
            self.lab.sched(window_seconds=10)
        raise AssertionError("Channels did not qualify from genuine tasks")

    def matrix(self, prefix="matrix"):
        self.pool(range(1, 21))
        for name, videos, input_seconds in (("text", [], 0), ("fraction", ["https://inputmedia:8443/fraction.mp4"], 2.5),
                                              ("twenty", ["https://inputmedia:8443/eight.mp4", "https://inputmedia:8443/twelve.mov"], 20)):
            cases = []
            for tier in TIERS:
                for seconds in SECONDS:
                    case = {"resolution": tier, "seconds": seconds, "videos": videos, "input_seconds": input_seconds}
                    case["oracle"] = expected_candidates(case)
                    case["expected"] = case["oracle"]["numbers"]
                    cases.append(case)
            self.phase(prefix + "-" + name, cases)

    def boundaries(self):
        self.pool(range(1, 21))
        cases = [{"seconds": seconds, "resolution": "720p", "reject": True} for seconds in (0, 3, 16)]
        cases += [{"seconds": 5, "resolution": "1024", "reject": True},
                  {"seconds": 5, "resolution": "720p", "size": "1920x1080", "reject": True},
                  {"seconds": 4, "resolution": "720p", "group": "seedance-discount", "ratio": .1, "reject": True}]
        self.phase("boundaries-and-margin", cases)
        self.phase("multipart-and-4k-alias", [
            {"seconds": 4, "resolution": "720p", "multipart": True, "expected": [17]},
            {"seconds": 15, "resolution": "2160p", "size": SIZES["4k"], "expected": [6]}])
        invalid = {"seconds": 4, "resolution": "720p", "videos": ["https://inputmedia:8443/invalid.mp4"], "input_seconds": None}
        invalid["expected"] = expected_candidates(invalid)["numbers"]
        self.phase("unreadable-reference-fallback", [invalid])

    def contracts(self):
        self.pool(range(1, 21))
        cases = []
        for profile in PROFILES:
            number, cost = profile["number"], profile["cost"]
            tier = next(iter(cost["prices"]))
            case = {"seconds": cost["allowed_seconds_by_resolution"][tier][0], "resolution": tier,
                    "group": f"seedance-{number:02d}", "videos": ["https://inputmedia:8443/fraction.mp4"], "input_seconds": 2.5}
            case["oracle"] = expected_candidates(case, [number])
            case["expected"] = case["oracle"]["numbers"]
            if number in (2, 5):
                case["reject"] = True
            cases.append(case)
        duplicate = {"seconds": 4, "resolution": "480p", "group": "seedance-18",
                     "videos": ["https://inputmedia:8443/fraction.mp4"] * 2, "input_seconds": 5}
        duplicate["oracle"] = expected_candidates(duplicate, [18])
        duplicate["expected"] = [18]
        self.phase("all-upstream-reference-contracts", cases + [duplicate])

    def policies(self):
        self.pool([6, 14])
        self.configure(6, priority=10)
        self.phase("priority-before-cost", [{"seconds": 4, "resolution": "720p", "expected": [6]}])
        self.configure(6, priority=0, quality=.98)
        self.phase("quality-before-cost", [{"seconds": 4, "resolution": "720p", "expected": [6]}])
        self.configure(6, quality=.9)
        costly = deepcopy(PROFILES[5]["cost"])
        costly["prices"] = {tier: 999 for tier in costly["prices"]}
        self.configure(6, priority=10, cost=costly)
        self.phase("priority-cannot-bypass-margin", [{"seconds": 4, "resolution": "720p", "expected": [14]}])
        self.configure(6, priority=0, cost=deepcopy(PROFILES[5]["cost"]))
        self.pool([14])
        self.mock(14, finish_ms=20000)
        self.phase("sale-snapshot-freeze", [{"seconds": 7, "resolution": "720p", "expected": [14]}], after_submit=lambda: self.sale(multiplier=2))
        self.phase("changed-sale-applies-to-next-request", [{"seconds": 7, "resolution": "720p", "sale_multiplier": 2, "expected": [14]}])
        self.sale()
        self.mock(14)
        self.pool(range(1, 21))

    def modes(self):
        self.pool([6])
        try:
            for mode in ("off", "shadow"):
                self.lab.sched(mode=mode)
                self.phase("mode-" + mode, [{"seconds": 5, "resolution": "720p", "reject": True}])
        finally:
            self.lab.sched(mode="on")
            self.pool(range(1, 21))
        self.qualify(suffix="after-mode-switch")

    def faults(self):
        for number, mode in ((6, "reject500"), (7, "reject401"), (8, "reject429"), (13, "reject200")):
            self.pool([number, 14])
            self.configure(number, priority=10)
            self.mock(number, mode)
            self.mock(14)
            phase = self.phase("submit-" + mode, [{"seconds": 5, "resolution": "720p", "expected": [14]}] * 2, concurrency=1)
            evidence = json.loads((self.results / f"{phase['label']}-evidence.json").read_text(encoding="utf-8"))
            self.check(phase, "faulty upstream was actually exercised", any(e["event"] == "submit" and e["number"] == number for e in evidence["events"]))
            self.save_phase(phase, finalized=True)
            self.configure(number, priority=0)
            self.mock(number)
        self.pool(range(1, 21))
        # Pidoi deliberately receives an unsupported status here: its declared
        # protocol has no cancelled status. This exercises bounded poll failure;
        # a supported user cancellation is tested separately with MeAI.
        failures = [(9, "user_failure", "480p"), (10, "cancelled", "720p"), (11, "generation_failure", "1080p"),
                    (15, "poll500", "720p"), (16, "poll404", "720p")]
        cases = []
        for number, mode, tier in failures:
            self.mock(number, mode)
            cases.append({"seconds": 5, "resolution": tier, "group": f"seedance-{number:02d}", "expected": [number], "terminal": "FAILURE"})
        self.phase("terminal-failure-and-refund", cases)
        for number, _, _ in failures:
            self.mock(number)
        for number, mode in ((17, "drop_ack"), (18, "malformed_ack"), (19, "hold_ack")):
            self.pool([number, 14])
            self.configure(number, priority=10)
            self.mock(number, "healthy" if mode == "hold_ack" else mode, hold_ack=mode == "hold_ack")
            self.phase("uncertain-" + mode, [{"seconds": 5, "resolution": "720p", "unknown": True}])
            self.configure(number, priority=0)
            self.mock(number)
        self.pool(range(1, 21))

    def capacity(self):
        self.pool([20, 14])
        self.configure(20, priority=10, capacity=2)
        self.mock(20, submit_ms=200, finish_ms=20000, max_active=2)
        self.mock(14)
        phase = self.phase("capacity-limit", [{"seconds": 5, "resolution": "720p", "expected": [20, 14]}] * 12, concurrency=12)
        evidence = json.loads((self.results / f"{phase['label']}-evidence.json").read_text(encoding="utf-8"))
        limited = [e for e in evidence["events"] if e["event"] == "limited"]
        accepted = Counter(e["number"] for e in evidence["events"] if e["event"] == "accepted")
        self.check(phase, "capacity admits exactly two and routes ten to fallback", accepted == {20: 2, 14: 10} and not limited, dict(accepted))
        self.save_phase(phase, finalized=True)
        self.configure(20, capacity=0)
        phase = self.phase("upstream-429-fallback", [{"seconds": 5, "resolution": "720p", "expected": [20, 14]}] * 12, concurrency=12)
        evidence = json.loads((self.results / f"{phase['label']}-evidence.json").read_text(encoding="utf-8"))
        self.check(phase, "unbounded gateway actually encounters upstream 429", any(e["event"] == "limited" for e in evidence["events"]))
        self.check(phase, "429 does not block healthy upstream", self.state([20])[0]["state"] == "normal")
        self.save_phase(phase, finalized=True)
        self.configure(20, priority=0)
        self.mock(20)
        self.pool([12, 13, 14])
        self.lab.sched(capacity_groups={"seedance-shared": 2})
        for number in (12, 13):
            self.configure(number, priority=10, capacity_group="seedance-shared")
            self.mock(number, finish_ms=20000)
        phase = self.phase("shared-account-capacity", [{"seconds": 5, "resolution": "4k", "expected": [12, 13, 14]}] * 12, concurrency=12)
        evidence = json.loads((self.results / f"{phase['label']}-evidence.json").read_text(encoding="utf-8"))
        accepted = Counter(e["number"] for e in evidence["events"] if e["event"] == "accepted")
        self.check(phase, "two channels share exactly two generation slots", accepted[12] + accepted[13] == 2 and accepted[14] == 10, dict(accepted))
        self.save_phase(phase, finalized=True)
        for number in (12, 13):
            self.configure(number, priority=0, capacity_group="")
            self.mock(number)
        self.lab.sched(capacity_groups={})
        self.pool(range(1, 21))

    def recover(self):
        self.pool([3])
        self.mock(3, "cancelled")
        self.phase("supported-user-cancellation", [{"seconds": 5, "resolution": "720p", "group": "seedance-03", "expected": [3], "terminal": "FAILURE"}])
        self.mock(3)
        classification = {"checks": [], "attempts": {}}
        labels = ("submit-reject500", "submit-reject401", "submit-reject429", "submit-reject200",
                  "terminal-failure-and-refund", "supported-user-cancellation", "uncertain-drop_ack", "uncertain-malformed_ack", "uncertain-hold_ack")
        for label in labels:
            phases = [phase for phase in self.report["phases"] if phase["label"].endswith(label)]
            if not phases:
                raise AssertionError("Missing fault evidence: " + label)
            evidence = json.loads((self.results / f"{phases[-1]['label']}-evidence.json").read_text(encoding="utf-8"))
            attempts = self.lab.rows(f"SELECT channel_id,submit_outcome,final_outcome,attribution FROM video_health_attempts WHERE id>{evidence['before']['attempt_id']} AND id<={evidence['after']['attempt_id']} ORDER BY id")
            classification["attempts"][label] = attempts
            if label == "submit-reject429":
                target = self.channels[7]["id"]
                passed = any(row["channel_id"] == target and row["attribution"] == "rate_limited" for row in attempts) and not any(row["channel_id"] == target and row["final_outcome"] == "upstream" for row in attempts)
            elif label.startswith("submit-"):
                passed = any(row["submit_outcome"] == "rejected" and row["final_outcome"] == "upstream" for row in attempts)
            elif label.startswith("uncertain-"):
                passed = len(attempts) == 1 and attempts[0]["submit_outcome"] == "unknown" and attempts[0]["final_outcome"] == "unknown"
            elif label == "supported-user-cancellation":
                passed = len(attempts) == 1 and attempts[0]["submit_outcome"] == "accepted" and attempts[0]["final_outcome"] == "cancelled"
            else:
                expected = {self.channels[number-1]["id"]: outcome for number, outcome in ((9, "user"), (10, "upstream"), (11, "upstream"), (15, "upstream"), (16, "upstream"))}
                passed = len(attempts) == 5 and all(row["submit_outcome"] == "accepted" and row["final_outcome"] == expected[row["channel_id"]] for row in attempts)
            classification["checks"].append({"name": label + " health attribution", "passed": passed})
        classification["checks"].append({"name": "user cancellation and policy failure preserve normal provider health",
                                          "passed": all(state["state"] == "normal" for state in self.state([3, 9]))})
        self.report["fault_classification"] = classification
        write_json(self.results / "fault-classification.json", classification)
        self.save()
        if not all(check["passed"] for check in classification["checks"]):
            raise AssertionError("Fault attribution checks failed; see fault-classification.json")
        self.pool([6, 7])
        self.lab.sched(explore_share=0, probe_ratio=0)
        states = self.state([6, 7])
        if len(states) != 2 or any(state["state"] == "normal" for state in states):
            raise AssertionError("The all-blocked scenario requires two channels without normal admission")
        # With no normal candidate, bounded recovery probes are permitted even
        # at probe_ratio=0. Keep the actual upstreams unavailable for this case.
        for number in (6, 7):
            self.mock(number, "reject500")
        self.phase("all-blocked-no-charge", [{"seconds": 5, "resolution": "720p", "rejected_upstream": True}] * 3, concurrency=1)
        for number in (6, 7):
            self.mock(number)
        self.pool(range(1, 21))
        reviewed = []
        unknown = self.lab.rows("SELECT id,channel_id,request_id,final_outcome,reviewed_at FROM video_health_attempts WHERE final_outcome='unknown' AND coalesce(reviewed_at,0)=0 ORDER BY id")
        for attempt in unknown:
            # Review acknowledges uncertainty; it never fabricates success or
            # charges a client for work that returned no public task receipt.
            self.lab.api("POST", f"/api/channel/video_schedule/health_attempts/{attempt['id']}/review",
                         {"note": "Laboratory injected acknowledgement loss; mock acceptance is recorded, no public task receipt was returned and no client charge is retained"})
            reviewed.append(attempt["id"])
        recovery = []
        for state in self.state():
            if state["state"] != "blocked":
                continue
            reply = self.lab.api("POST", f"/api/channel/{state['channel_id']}/video_health/recover",
                                {"model": MODEL, "state_version": state["version"], "note": "Mock fault removed; require new real successful probe tasks before normal routing"})
            recovery.append({"channel": state["channel_id"], "before": state["state"], "after": reply["state"]})
            if reply["state"] == "normal":
                raise AssertionError("Manual recovery must not directly certify a blocked channel")
        self.report["operator_recovery"] = {"reviewed_unknown_attempts": reviewed, "requests": recovery}
        self.lab.sched(probe_max_in_flight=4)
        self.qualify(suffix="restored")
        self.lab.sched(probe_max_in_flight=1)
        self.matrix(prefix="restored")
        stats = requests.get(f"http://127.0.0.1:{self.lab.port_base+5}/_stats", timeout=10).json()
        self.report["media_fetch_metrics"] = stats
        if stats["credential_headers"] != 0:
            raise AssertionError("Reference media received gateway credentials")
        self.report["final_health"] = self.state()
        required = [
            "matrix-text", "matrix-fraction", "matrix-twenty", "all-upstream-reference-contracts",
            "boundaries-and-margin", "multipart-and-4k-alias", "unreadable-reference-fallback",
            "priority-before-cost", "quality-before-cost", "priority-cannot-bypass-margin",
            "sale-snapshot-freeze", "changed-sale-applies-to-next-request", "mode-off", "mode-shadow",
            "capacity-limit", "upstream-429-fallback", "shared-account-capacity",
            "submit-reject500", "submit-reject401", "submit-reject429", "submit-reject200",
            "terminal-failure-and-refund", "uncertain-drop_ack", "uncertain-malformed_ack", "uncertain-hold_ack",
            "supported-user-cancellation", "all-blocked-no-charge", "restored-text", "restored-fraction", "restored-twenty",
        ]
        requirements = []
        for name in required:
            # A diagnosed rerun uses a new prefix and keeps the earlier failed
            # evidence. Select the latest result for each explicit requirement.
            matching = [phase for phase in self.report["phases"] if phase["label"].endswith(name)]
            last = matching[-1] if matching else None
            requirements.append({"requirement": name, "phase": last["label"] if last else None,
                                 "passed": bool(last and last.get("finalized") and last["checks"] and all(check["passed"] for check in last["checks"]))})
        healthy = len(self.report["final_health"]) == 20 and all(state["state"] == "normal" and state["integrity"] == "complete" for state in self.report["final_health"])
        self.report["requirements"] = requirements
        self.report["all_checks_passed"] = healthy and all(item["passed"] for item in requirements)
        self.report["complete"] = self.report["all_checks_passed"]
        self.report["earlier_failed_trials"] = [{"label": phase["label"], "failed_checks": [check["name"] for check in phase["checks"] if not check["passed"]]}
                                              for phase in self.report["phases"] if any(not check["passed"] for check in phase["checks"])]
        self.save()
        if not self.report["complete"]:
            raise AssertionError("Required verification is incomplete; see summary requirements")


def start_fixture(args):
    fixture = Fixture(args.label, existing=args.resume, port_base=args.port_base)
    if not args.resume:
        fixture.prepare()
        path = fixture.folder / "compose.private.json"
        compose = json.loads(path.read_text(encoding="utf-8"))
        for profile in PROFILES:
            compose["services"][f"mock{profile['number']:02d}"]["environment"]["PROVIDER"] = profile["plugin"]
        app = compose["services"]["app"]
        app["environment"]["SSL_CERT_FILE"] = "/fixture-tls/bundle.pem"
        app["volumes"].append({"type": "bind", "source": str(REPO / ".scratch/video-load-lab/input-media-tls/bundle.pem"), "target": "/fixture-tls/bundle.pem", "read_only": True})
        write_json(path, compose)
    path = fixture.folder / "compose.private.json"
    compose = json.loads(path.read_text(encoding="utf-8"))
    for service in compose["services"].values():
        for volume in service.get("volumes", []):
            if isinstance(volume, dict) and volume.get("type") == "bind":
                volume["source"] = rancher_mount_path(volume["source"])
    write_json(path, compose)
    fixture.start()
    media_name = fixture.project + "-inputmedia"
    inspected = fixture._run(["docker", "ps", "-a", "--filter", "name=^/" + media_name + "$", "--format", "{{.ID}}"], check=True)
    if not inspected.stdout.strip():
        fixture._run(["docker", "run", "-d", "--name", media_name, "--network", fixture.project + "_default", "--network-alias", "inputmedia",
                      "--label", "codex.fixture=unified-video-stress", "--label", "codex.fixture.project=" + fixture.project,
                      "--memory", "96m", "--pids-limit", "64", "-p", f"127.0.0.1:{fixture.port_base+5}:8080",
                      "--mount", "type=bind,src=" + rancher_mount_path(REPO / ".scratch/video-load-lab/input-media") + ",dst=/data,readonly",
                      "--mount", "type=bind,src=" + rancher_mount_path(REPO / ".scratch/video-load-lab/input-media-tls") + ",dst=/tls,readonly",
                      "codex-video-lab-input-media:20261002"])
    else:
        labels = fixture._run(["docker", "inspect", "--format", "{{json .Config.Labels}}", inspected.stdout.strip()])
        if json.loads(labels.stdout).get("codex.fixture.project") != fixture.project:
            raise RuntimeError("Media container belongs to another fixture")
        fixture._run(["docker", "start", inspected.stdout.strip()])
    fixture.metadata.update({"fixture_kind": "seedance-20-unified-sales", "profiles": PROFILES,
                             "providers": sorted({profile["plugin"] for profile in PROFILES}),
                             "upstream_models": sorted({profile["upstream_model"] for profile in PROFILES})})
    fixture._save_metadata()
    return fixture


def rancher_mount_path(path):
    value = str(path).replace("\\", "/")
    if os.name == "nt" and len(value) > 2 and value[1:3] == ":/":
        return "/mnt/" + value[0].lower() + value[2:]
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--label", required=True)
    parser.add_argument("--port-base", type=int, default=35000)
    parser.add_argument("--resume", action="store_true")
    parser.add_argument("--qualification-label", default="initial")
    parser.add_argument("--phase-prefix", default="", help="New evidence namespace after diagnosing a failed phase")
    parser.add_argument("--stage", choices=("prepare", "qualify", "matrix", "contracts", "boundaries", "policies", "modes", "faults", "capacity", "recover", "all"), default="all")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,48}", args.qualification_label) or not re.fullmatch(r"[A-Za-z0-9_-]{0,48}", args.phase_prefix):
        parser.error("Evidence labels accept only ASCII letters, digits, underscores and hyphens")
    fixture = start_fixture(args)
    runner = SeedanceRunner(fixture, args)
    runner.bootstrap()
    if args.stage in ("qualify", "all"):
        runner.qualify(suffix=args.qualification_label)
    if args.stage in ("matrix", "all"):
        runner.matrix()
    for stage in ("contracts", "boundaries", "policies", "modes", "capacity", "faults", "recover"):
        if args.stage in (stage, "all"):
            getattr(runner, stage)()
    runner.save()
    print(json.dumps({"base": fixture.base, "report": str(runner.report_path), "services_retained_running": True}), flush=True)


if __name__ == "__main__":
    main()
