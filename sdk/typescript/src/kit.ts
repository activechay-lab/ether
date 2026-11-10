/**
 * Kit helpers for content-kind plugins (stdlib/runtime only, matching
 * sdk/go/kit): feed parsing, zip member listing, and simple HTML text
 * and link extraction for listing pages.
 */

export interface FeedEntry {
  id: string;
  title: string;
  summary: string;
  link: string;
  audio: string;
  date: string;
}

export interface ParsedFeed {
  title: string;
  summary: string;
  link: string;
  entries: FeedEntry[];
}

function tagText(block: string, name: string): string {
  const m = new RegExp(`<${name}(?:\\s[^>]*)?>([\\s\\S]*?)</${name}>`, "i").exec(block);
  return m ? m[1]!.replace(/<!\[CDATA\[([\s\S]*?)\]\]>/, "$1").trim() : "";
}

function blocks(xml: string, name: string): string[] {
  return [...xml.matchAll(new RegExp(`<${name}(?:\\s[^>]*)?>([\\s\\S]*?)</${name}>`, "gi"))].map((m) => m[1]!);
}

/** Parse RSS, Atom or JSON Feed into a normalised feed. */
export function parseFeed(data: string): ParsedFeed {
  if (data.trimStart().startsWith("{")) {
    const doc = JSON.parse(data) as Record<string, any>;
    return {
      title: doc.title ?? "",
      summary: doc.description ?? "",
      link: doc.home_page_url ?? "",
      entries: (doc.items ?? []).map((it: Record<string, any>) => ({
        id: it.id || it.url || "",
        title: it.title ?? "",
        summary: it.summary ?? "",
        link: it.url ?? "",
        audio: it.attachments?.[0]?.url ?? "",
        date: it.date_published ?? "",
      })),
    };
  }
  if (/<feed[\s>]/.test(data)) {
    return {
      title: tagText(data, "title"),
      summary: tagText(data, "subtitle"),
      link: "",
      entries: blocks(data, "entry").map((e) => {
        let page = "";
        let media = "";
        for (const m of e.matchAll(/<link[^>]*href="([^"]+)"[^>]*>/gi)) {
          if (/rel="enclosure"/.test(m[0])) media = m[1]!;
          else if (!page) page = m[1]!;
        }
        return { id: tagText(e, "id") || page, title: tagText(e, "title"), summary: tagText(e, "summary"), link: page, audio: media, date: tagText(e, "updated") };
      }),
    };
  }
  const channel = blocks(data, "channel")[0] ?? data;
  return {
    title: tagText(channel.split("<item")[0]!, "title"),
    summary: tagText(channel.split("<item")[0]!, "description"),
    link: tagText(channel.split("<item")[0]!, "link"),
    entries: blocks(data, "item").map((e) => ({
      id: tagText(e, "guid") || tagText(e, "link"),
      title: tagText(e, "title"),
      summary: tagText(e, "description"),
      link: tagText(e, "link"),
      audio: /<enclosure[^>]*url="([^"]+)"/.exec(e)?.[1] ?? "",
      date: tagText(e, "pubDate"),
    })),
  };
}

/** One non-directory zip member, in archive order. */
export interface ArchiveMember {
  index: number;
  name: string;
  size: number;
}

/** List zip/cbz/epub members by reading the central directory. */
export function archiveList(data: Uint8Array): ArchiveMember[] {
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
  let eocd = -1;
  for (let i = data.length - 22; i >= 0; i--) {
    if (view.getUint32(i, true) === 0x06054b50) { eocd = i; break; }
  }
  if (eocd < 0) throw new Error("archiveList: not a zip archive");
  const count = view.getUint16(eocd + 10, true);
  let off = view.getUint32(eocd + 16, true);
  const decoder = new TextDecoder();
  const out: ArchiveMember[] = [];
  for (let n = 0; n < count && off + 46 <= data.length; n++) {
    if (view.getUint32(off, true) !== 0x02014b50) break;
    const size = view.getUint32(off + 24, true);
    const nameLen = view.getUint16(off + 28, true);
    const extraLen = view.getUint16(off + 30, true);
    const commentLen = view.getUint16(off + 32, true);
    const name = decoder.decode(data.subarray(off + 46, off + 46 + nameLen));
    if (!name.endsWith("/")) out.push({ index: out.length, name, size });
    off += 46 + nameLen + extraLen + commentLen;
  }
  return out;
}

/** Strip markup down to readable text. */
export function scrapeText(html: string): string {
  return html
    .replace(/<script[\s\S]*?<\/script>|<style[\s\S]*?<\/style>|<[^>]+>/gi, " ")
    .replace(/\s+/g, " ")
    .trim();
}

/** Extract anchors with absolute URLs resolved against base. */
export function scrapeLinks(html: string, base: string): Array<{ url: string; text: string }> {
  const out: Array<{ url: string; text: string }> = [];
  for (const m of html.matchAll(/<a\s[^>]*href\s*=\s*["']([^"']+)["'][^>]*>([\s\S]*?)<\/a>/gi)) {
    const href = m[1]!.trim();
    if (!href || href.startsWith("#") || href.startsWith("javascript:")) continue;
    out.push({ url: new URL(href, base).toString(), text: scrapeText(m[2]!) });
  }
  return out;
}
