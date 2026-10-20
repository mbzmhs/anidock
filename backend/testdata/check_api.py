#!/usr/bin/env python3
import json, urllib.request

def get(path):
    with urllib.request.urlopen("http://localhost:8080" + path) as r:
        return json.load(r)

print("--- releases (latest 30) ---")
for r in get("/api/latest?limit=30"):
    ep = r.get("episode")
    print(f'ep={ep} group={r.get("group_name")!r} feed={r.get("feed_name")!r} '
          f'anime_id={r.get("anime_id")} qual={r.get("quality")} dl={"yes" if r.get("download_url") else "-"}')
    print(f'    title={r.get("title")[:80]}')

print("\n--- episodes (anime 2, 每集去重) ---")
for e in get("/api/anime/2/episodes"):
    b = e["best"]
    print(f'第{e["episode"]}集 -> {b.get("feed_name")} (prio {b.get("feed_priority")}) '
          f'{b.get("spec") or b.get("quality")} | url={b.get("url")}')
    for a in e.get("alternatives", []):
        print(f'   其它源: {a.get("feed_name")} {a.get("quality")}')

print("\n--- anime list ---")
for a in get("/api/anime"):
    print(a["id"], a["title"], "| latest_ep:", a.get("latest_episode"),
          "| group:", a.get("latest_group"), "| updated:", a.get("latest_updated_at"),
          "| ep_count:", a.get("episode_count"))
