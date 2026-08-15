"use client";

import { useState } from "react";
import { VideoStatus } from "@/components/video-status";
import { HlsPlayer } from "@/components/hls-player";
import type { VideoWithRenditions } from "@/lib/api";

export function WatchPlayerArea({
  video: initial,
}: {
  video: VideoWithRenditions;
}) {
  const [video, setVideo] = useState(initial);

  if (video.status !== "ready" || !video.masterManifestUrl) {
    return <VideoStatus video={video} onReady={setVideo} />;
  }

  const renditions = video.renditions
    .slice()
    .sort((a, b) => b.height - a.height);

  return (
    <div className="flex flex-col gap-4">
      <HlsPlayer src={video.masterManifestUrl} />
      {renditions.length > 0 ? (
        <p className="text-sm text-muted-foreground">
          Produced renditions:{" "}
          {renditions
            .map((r) => `${r.height}p (${r.bitrateKbps} kbps)`)
            .join(", ")}
        </p>
      ) : null}
    </div>
  );
}
