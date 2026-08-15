"use client";

import { useEffect, useState } from "react";
import { getVideo, type VideoWithRenditions } from "@/lib/api";

const POLL_INTERVAL_MS = 2000;
const STEPS = ["uploading", "queued", "processing", "ready"] as const;

export function VideoStatus({
  video: initialVideo,
  onReady,
}: {
  video: VideoWithRenditions;
  onReady?: (video: VideoWithRenditions) => void;
}) {
  const [video, setVideo] = useState(initialVideo);

  useEffect(() => {
    if (video.status === "ready" || video.status === "failed") return;

    let cancelled = false;
    const interval = setInterval(async () => {
      const next = await getVideo(video.id);
      if (cancelled || !next) return;
      setVideo(next);
      if (next.status === "ready") onReady?.(next);
    }, POLL_INTERVAL_MS);

    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [video.id, video.status, onReady]);

  if (video.status === "failed") {
    return (
      <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive">
        Processing failed{video.failureReason ? `: ${video.failureReason}` : "."}
      </div>
    );
  }

  const stepIndex = STEPS.indexOf(video.status as (typeof STEPS)[number]);

  return (
    <div
      className="flex items-center gap-2 text-sm"
      data-testid="status-stepper"
      data-status={video.status}
    >
      {STEPS.map((step, i) => (
        <span key={step} className="flex items-center gap-2">
          <span
            className={
              i <= stepIndex
                ? "font-medium text-foreground"
                : "text-muted-foreground"
            }
          >
            {step}
          </span>
          {i < STEPS.length - 1 ? (
            <span className="text-muted-foreground">→</span>
          ) : null}
        </span>
      ))}
    </div>
  );
}
