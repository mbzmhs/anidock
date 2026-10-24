#!/usr/bin/env python3
"""本地模拟字幕组 RSS 源（真实订阅站 URL 本地不可达，用本服务跑通全链路）。
per-show 源：/feed/b（字幕组B，优先级1，2160P 慢更）、/feed/g（字幕组G，720p 快更）、
/feed/c（字幕组C，回归场景：合集区间/旧季拦截/接续编号/跨季包）。
"""
import http.server

FEED_B = """<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0"><channel>
<title>rss - 字幕组B - 番剧A 第二季</title>
<item>
  <title>【字幕组B】番剧A 第二季 第1集 [2160P][HEVC]</title>
  <link>https://example.org/detail/an1-ep1</link>
  <description>详情见上。磁力: magnet:?xt=urn:btih:FEEDBEP100000000000000000000000000000001&amp;dn=feedb-ep1</description>
  <pubDate>Mon, 12 Oct 2026 08:00:00 GMT</pubDate>
</item>
<item>
  <title>【字幕组B】番剧A 第二季 第2集 [2160P][HEVC]</title>
  <link>https://example.org/detail/an1-ep2</link>
  <description>详情见上。磁力: magnet:?xt=urn:btih:FEEDBEP100000000000000000000000000000002&amp;dn=feedb-ep2</description>
  <pubDate>Mon, 12 Oct 2026 09:00:00 GMT</pubDate>
</item>
<item>
  <title>【字幕组B】番剧A 第二季 第3集 [1080P][x265]</title>
  <link>https://example.org/detail/an1-ep3</link>
  <description>详情见上</description>
  <pubDate>Tue, 13 Oct 2026 08:30:00 GMT</pubDate>
</item>
</channel></rss>"""

FEED_G = """<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0"><channel>
<title>rss - 字幕组G - 番剧A 第二季</title>
<item>
  <title>【字幕组G】番剧A 第二季 Episode 1 [1080P] (简繁 示例视频台)</title>
  <link>https://example.org/detail/g-ep1</link>
  <description>详情见上</description>
  <pubDate>Sun, 11 Oct 2026 20:00:00 GMT</pubDate>
</item>
<item>
  <title>【字幕组G】番剧A 第二季 第2集 720p 简繁</title>
  <link>https://example.org/detail/g-ep2</link>
  <description>详情见上</description>
  <pubDate>Mon, 12 Oct 2026 12:00:00 GMT</pubDate>
</item>
<item>
  <title>【字幕组G】番剧A 第二季 第4集 720p 简繁</title>
  <link>https://example.org/detail/g-ep4</link>
  <description>详情见上</description>
  <pubDate>Wed, 14 Oct 2026 10:00:00 GMT</pubDate>
</item>
</channel></rss>"""

# FEED_C 回归场景：合集区间 / 旧季条目（无季词+早于播出窗口）/ 接续编号本季条目 / 跨季合集包
FEED_C = """<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0"><channel>
<title>rss - 字幕组C - 番剧A 第二季</title>
<item>
  <title>【字幕组C】番剧A 第二季 [01-12] [1080P][合集][简繁]</title>
  <link>https://example.org/detail/c-col-1-12</link>
  <description>详情见上</description>
  <pubDate>Fri, 16 Oct 2026 08:00:00 GMT</pubDate>
</item>
<item>
  <title>【字幕组C】番剧A 第1集 [1080P][简繁]</title>
  <link>https://example.org/detail/c-s1-ep1</link>
  <description>详情见上（旧季条目：无季词，发布于本季开播窗口前，应被时间兜底拦截）</description>
  <pubDate>Sun, 05 Jan 2025 08:00:00 GMT</pubDate>
</item>
<item>
  <title>【字幕组C】番剧A 第13集 [1080P][简繁]</title>
  <link>https://example.org/detail/c-s2-ep13</link>
  <description>详情见上（接续编号的本季条目：无季词，窗口内放行）</description>
  <pubDate>Mon, 12 Oct 2026 08:00:00 GMT</pubDate>
</item>
<item>
  <title>【字幕组C】番剧A 第一季+第二季 合集 [1080P]</title>
  <link>https://example.org/detail/c-pack-multi-season</link>
  <description>详情见上（跨季合集包：应被季词过滤拦截）</description>
  <pubDate>Mon, 12 Oct 2026 09:00:00 GMT</pubDate>
</item>
</channel></rss>"""


class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/feed/b"):
            body = FEED_B.encode()
        elif self.path.startswith("/feed/c"):
            body = FEED_C.encode()
        else:
            body = FEED_G.encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/rss+xml; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass

http.server.HTTPServer(("127.0.0.1", 9091), H).serve_forever()
