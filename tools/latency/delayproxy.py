#!/usr/bin/env python3
"""TCP proxy that delays every chunk by a fixed one-way latency (both
directions), to emulate a WAN hop without root (no tc/netem).
Usage: delayproxy.py LISTEN_PORT TARGET_HOST:PORT ONE_WAY_MS [JITTER_MS]"""
import asyncio, random, sys

listen, target, delay = int(sys.argv[1]), sys.argv[2], float(sys.argv[3]) / 1000
jitter = float(sys.argv[4]) / 1000 if len(sys.argv) > 4 else 0.0
th, tp = target.rsplit(":", 1)

async def pipe(reader, writer):
    q = asyncio.Queue()
    async def pump():
        last = 0.0
        while True:
            due, data = await q.get()
            if data is None:
                break
            now = asyncio.get_running_loop().time()
            await asyncio.sleep(max(0, due - now))
            writer.write(data)
            await writer.drain()
        writer.close()
    task = asyncio.create_task(pump())
    loop = asyncio.get_running_loop()
    last_due = 0.0
    try:
        while data := await reader.read(65536):
            due = max(loop.time() + delay + random.uniform(0, jitter), last_due)  # keep order (TCP)
            last_due = due
            await q.put((due, data))
    finally:
        await q.put((0, None))
        await task

async def handle(cr, cw):
    sr, sw = await asyncio.open_connection(th, int(tp))
    await asyncio.gather(pipe(cr, sw), pipe(sr, cw), return_exceptions=True)

async def main():
    srv = await asyncio.start_server(handle, "127.0.0.1", listen)
    async with srv:
        await srv.serve_forever()

asyncio.run(main())
