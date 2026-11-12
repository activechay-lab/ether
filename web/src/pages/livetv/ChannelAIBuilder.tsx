import type { AgentMessage } from "@braedonsaunders/appkit-ai/react";
import { AskEther } from "../../components/agent/AskEther";
import { LookTile } from "./onair/Stage";
import type { Look } from "./onair/model";
import type { Channel } from "./model";
import { useEffect, useState } from "react";
import { tv, type Programme } from "./model";
import { ScheduleTimeline, type TimelineResponse } from "./ScheduleTimeline";
import { errorMessage } from "../../lib/api";
function SchedulePreview({ channel }: { channel: Channel }) {
  const [data, setData] = useState<TimelineResponse>();
  const [error, setError] = useState("");
  const [range] = useState(() => ({ from: Date.now(), to: Date.now() + 6 * 3600000 }));
  const key = JSON.stringify(channel);
  useEffect(() => {
    const ctl = new AbortController();
    setData(undefined);
    setError("");
    tv.preview(JSON.parse(key) as Channel, ctl.signal, {
      from: new Date(range.from),
      until: new Date(range.to),
    })
      .then((programmes: Programme[]) => {
        setData({
          channelId: channel.id || "draft",
          timezone: channel.timezone || "UTC",
          now: range.from,
          dayparts: [],
          elements: programmes.map((p) => ({
            id: p.id,
            kind: "programme",
            start: Date.parse(p.start),
            end: Date.parse(p.end),
            title: p.item.title,
            rule: p.reason || "",
          })),
        });
      })
      .catch((e) => {
        if (!ctl.signal.aborted) setError(errorMessage(e));
      });
    return () => ctl.abort();
  }, [key, range]);
  return error ? (
    <p role="alert">{error}</p>
  ) : data ? (
    <ScheduleTimeline channelId={channel.id || "draft"} snapshot={data} range={range} />
  ) : null;
}

function preview(messages: AgentMessage[]) {
  let channel: Channel | undefined;
  let style: Look | undefined;
  for (const message of messages)
    for (const raw of message.parts) {
      const part = raw as unknown as { output?: { channel?: Channel; style?: Look; look?: Look } };
      if (part.output?.channel && typeof part.output.channel === "object")
        channel = part.output.channel;
      if (part.output?.style) style = part.output.style;
      if (part.output?.look?.scenes) style = part.output.look;
    }
  return (
    <div className="max-h-[40vh] overflow-auto px-4">
      {style?.scenes && <LookTile look={style} playOnHover />}
      {channel && <SchedulePreview channel={channel} />}
    </div>
  );
}
export function ChannelAIBuilder({ channel }: { channel?: Channel }) {
  return (
    <AskEther
      key={channel?.id || "new"}
      builder={channel ? "refine" : "create"}
      draft={channel ? { channel } : undefined}
      preview={preview}
    />
  );
}
