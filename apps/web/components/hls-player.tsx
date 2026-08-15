"use client";

import { useEffect, useRef, useState } from "react";
import Hls, {
  Events,
  ErrorTypes,
  type ErrorData,
  type LevelSwitchedData,
  type ManifestParsedData,
} from "hls.js";

interface LevelInfo {
  index: number;
  height: number;
  bitrateKbps: number;
}

export function HlsPlayer({ src }: { src: string }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const hlsRef = useRef<Hls | null>(null);
  const [levels, setLevels] = useState<LevelInfo[]>([]);
  const [activeLevel, setActiveLevel] = useState<LevelInfo | null>(null);
  const [selected, setSelected] = useState<number>(-1); // -1 = Auto
  const [error, setError] = useState<string | null>(null);
  const [unsupported, setUnsupported] = useState(false);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    if (!Hls.isSupported()) {
      if (video.canPlayType("application/vnd.apple.mpegurl")) {
        video.src = src;
      } else {
        setUnsupported(true);
      }
      return;
    }

    const hls = new Hls();
    hlsRef.current = hls;

    hls.on(Events.MANIFEST_PARSED, (_event, data: ManifestParsedData) => {
      setLevels(
        data.levels.map((level, index) => ({
          index,
          height: level.height,
          bitrateKbps: Math.round(level.bitrate / 1000),
        })),
      );
    });

    hls.on(Events.LEVEL_SWITCHED, (_event, data: LevelSwitchedData) => {
      const level = hls.levels[data.level];
      if (level) {
        setActiveLevel({
          index: data.level,
          height: level.height,
          bitrateKbps: Math.round(level.bitrate / 1000),
        });
      }
    });

    hls.on(Events.ERROR, (_event, data: ErrorData) => {
      if (!data.fatal) return;
      switch (data.type) {
        case ErrorTypes.NETWORK_ERROR:
          setError("Network error while loading the stream.");
          break;
        case ErrorTypes.MEDIA_ERROR:
          setError("Media error while playing the stream.");
          break;
        default:
          setError("Playback failed.");
          break;
      }
    });

    hls.loadSource(src);
    hls.attachMedia(video);

    return () => {
      hls.destroy();
      hlsRef.current = null;
    };
  }, [src]);

  function handleSelect(index: number) {
    setSelected(index);
    if (hlsRef.current) {
      hlsRef.current.currentLevel = index;
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <video ref={videoRef} controls className="w-full rounded-lg bg-black" />
      {unsupported ? (
        <p className="text-sm text-destructive">
          This browser can&apos;t play HLS video.
        </p>
      ) : error ? (
        <p className="text-sm text-destructive">{error}</p>
      ) : (
        <div className="flex items-center gap-3 text-sm text-muted-foreground">
          <span data-testid="active-rendition">
            {activeLevel
              ? `${activeLevel.height}p · ${activeLevel.bitrateKbps} kbps`
              : "Loading…"}
          </span>
          {levels.length > 0 ? (
            <select
              className="rounded border bg-background px-2 py-1 text-xs"
              value={selected}
              onChange={(e) => handleSelect(Number(e.target.value))}
            >
              <option value={-1}>Auto</option>
              {levels.map((level) => (
                <option key={level.index} value={level.index}>
                  {level.height}p · {level.bitrateKbps} kbps
                </option>
              ))}
            </select>
          ) : null}
        </div>
      )}
    </div>
  );
}
