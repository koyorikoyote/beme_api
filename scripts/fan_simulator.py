#!/usr/bin/env python3
"""
Fan Simulator — load-test the BEME chat endpoint with synthetic concurrent fan traffic.

Usage:
    python scripts/fan_simulator.py --url http://localhost:8080 --users 100 --messages 20 --rate 500
"""

import argparse
import asyncio
import json
import random
import time
from typing import List

import aiohttp

# ---------------------------------------------------------------------------
# Sample data pools
# ---------------------------------------------------------------------------

FIRST_NAMES = [
    "Alex", "Blake", "Casey", "Dana", "Ellis", "Finn", "Gray", "Harper",
    "Indigo", "Jordan", "Kai", "Lane", "Morgan", "Nova", "Oakley", "Parker",
    "Quinn", "Reese", "Sage", "Taylor", "Uma", "Vale", "Wren", "Xen", "Yael", "Zara",
]

EXPERTISE_POOL = ["fps", "strategy", "rpg", "moba", "sports", "puzzle"]

SAMPLE_MESSAGES = [
    "Try flanking from the left side!",
    "Use the high ground advantage here.",
    "Save your ultimate for the team fight.",
    "Build economy first before aggressing.",
    "Watch the mini-map more often.",
    "That's a great rotation strategy!",
    "You should prioritize objectives over kills.",
    "Nice mechanics, keep it up!",
    "Try a different item build this round.",
    "Communication is key in team fights.",
    "Don't forget to ward the river.",
    "Your positioning needs work in late game.",
    "Amazing play! That was insane.",
    "Consider a more defensive playstyle here.",
    "The enemy jungler is probably top right now.",
    "Push the wave before recalling.",
    "You're doing great, just stay patient.",
    "Bait the ability before engaging.",
    "Group up for the next objective.",
    "That's a textbook execute, well done!",
]


# ---------------------------------------------------------------------------
# Profile generation
# ---------------------------------------------------------------------------

def generate_profile() -> dict:
    """Generate a synthetic viewer profile matching the BEME chat POST schema."""
    viewer_id = f"viewer_{random.randint(1, 10000)}"
    display_name = f"{random.choice(FIRST_NAMES)}{random.randint(1, 99)}"
    contribution_score = random.randint(0, 100)
    num_tags = random.randint(0, len(EXPERTISE_POOL))
    expertise_tags = random.sample(EXPERTISE_POOL, num_tags)
    content = random.choice(SAMPLE_MESSAGES)

    return {
        "viewer_id": viewer_id,
        "display_name": display_name,
        "contribution_score": contribution_score,
        "expertise_tags": expertise_tags,
        "content": content,
    }


# ---------------------------------------------------------------------------
# Async worker
# ---------------------------------------------------------------------------

async def send_messages(
    session: aiohttp.ClientSession,
    url: str,
    num_messages: int,
    semaphore: asyncio.Semaphore,
    results: List[float],
    errors: List[str],
    rate_limiter: asyncio.Semaphore,
) -> None:
    """Send `num_messages` POST requests for a single simulated user."""
    endpoint = f"{url.rstrip('/')}/api/chat"

    for _ in range(num_messages):
        payload = generate_profile()

        async with semaphore:
            # Acquire a rate-limiter slot (released by the ticker task)
            await rate_limiter.acquire()

            start = time.perf_counter()
            try:
                async with session.post(
                    endpoint,
                    json=payload,
                    headers={"Content-Type": "application/json"},
                ) as resp:
                    await resp.read()  # drain body
                    elapsed_ms = (time.perf_counter() - start) * 1000
                    results.append(elapsed_ms)
            except Exception as exc:  # noqa: BLE001
                elapsed_ms = (time.perf_counter() - start) * 1000
                errors.append(str(exc))
                results.append(elapsed_ms)


# ---------------------------------------------------------------------------
# Rate-limiter ticker
# ---------------------------------------------------------------------------

async def rate_ticker(rate: float, total_requests: int, sem: asyncio.Semaphore) -> None:
    """Release `rate` permits per second into `sem` until all requests are covered."""
    interval = 1.0 / rate
    released = 0
    while released < total_requests:
        sem.release()
        released += 1
        await asyncio.sleep(interval)


# ---------------------------------------------------------------------------
# Main runner
# ---------------------------------------------------------------------------

async def run(url: str, users: int, messages: int, rate: float) -> None:
    total_requests = users * messages

    # Semaphore to cap concurrent in-flight HTTP connections
    concurrency_sem = asyncio.Semaphore(min(users * 2, 2000))

    # Rate-limiter semaphore — starts at 0; ticker releases tokens at `rate` RPS
    rate_sem = asyncio.Semaphore(0)

    results: List[float] = []
    errors: List[str] = []

    connector = aiohttp.TCPConnector(limit=0, ttl_dns_cache=300)
    timeout = aiohttp.ClientTimeout(total=30)

    wall_start = time.perf_counter()

    async with aiohttp.ClientSession(connector=connector, timeout=timeout) as session:
        # Start the rate-limiter ticker in the background
        ticker_task = asyncio.create_task(
            rate_ticker(rate, total_requests, rate_sem)
        )

        # Spawn one coroutine per simulated user
        user_tasks = [
            asyncio.create_task(
                send_messages(
                    session, url, messages, concurrency_sem, results, errors, rate_sem
                )
            )
            for _ in range(users)
        ]

        await asyncio.gather(*user_tasks)
        ticker_task.cancel()

    wall_duration = time.perf_counter() - wall_start

    # ---------------------------------------------------------------------------
    # Summary report
    # ---------------------------------------------------------------------------
    total_sent = len(results)
    total_errors = len(errors)
    throughput = total_sent / wall_duration if wall_duration > 0 else 0.0

    if results:
        avg_latency = sum(results) / len(results)
        sorted_latencies = sorted(results)
        p95_index = int(len(sorted_latencies) * 0.95)
        p95_latency = sorted_latencies[min(p95_index, len(sorted_latencies) - 1)]
    else:
        avg_latency = 0.0
        p95_latency = 0.0

    print("\n=== Fan Simulator Results ===")
    print(f"Total sent:     {total_sent}")
    print(f"Errors:         {total_errors}")
    print(f"Avg latency:    {avg_latency:.1f}ms")
    print(f"p95 latency:    {p95_latency:.1f}ms")
    print(f"Throughput:     {throughput:.1f} req/s")
    print(f"Duration:       {wall_duration:.2f}s")


# ---------------------------------------------------------------------------
# CLI entry point
# ---------------------------------------------------------------------------

def main() -> None:
    parser = argparse.ArgumentParser(
        description="BEME Fan Simulator — synthetic load test for the chat endpoint."
    )
    parser.add_argument(
        "--url",
        default="http://localhost:8080",
        help="Base URL of the BEME server (default: http://localhost:8080)",
    )
    parser.add_argument(
        "--users",
        type=int,
        default=100,
        help="Number of concurrent simulated users (default: 100)",
    )
    parser.add_argument(
        "--messages",
        type=int,
        default=20,
        help="Number of messages each user sends (default: 20)",
    )
    parser.add_argument(
        "--rate",
        type=float,
        default=100.0,
        help="Target request rate in requests/second (default: 100.0)",
    )

    args = parser.parse_args()

    print(f"Starting fan simulator: {args.users} users × {args.messages} messages "
          f"→ {args.users * args.messages} total requests @ {args.rate} RPS")
    print(f"Target: {args.url}/api/chat\n")

    asyncio.run(run(args.url, args.users, args.messages, args.rate))


if __name__ == "__main__":
    main()
