import { useEffect, useState } from "react";
import { apiBlob } from "../../../lib/api";
export function useAssetAudio(digest: string) {
  const [source, setSource] = useState("");
  useEffect(() => {
    setSource("");
    if (!/^[a-f0-9]{64}$/.test(digest)) return;
    const controller = new AbortController();
    let url = "";
    void apiBlob(`/ether/api/onair/assets/${digest}`, controller.signal)
      .then((blob) => {
        if (controller.signal.aborted) return;
        url = URL.createObjectURL(blob);
        setSource(url);
      })
      .catch(() => {});
    return () => {
      controller.abort();
      if (url) URL.revokeObjectURL(url);
    };
  }, [digest]);
  return source;
}
