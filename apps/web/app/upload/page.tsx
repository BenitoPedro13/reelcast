"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Progress } from "@/components/ui/progress";
import { VideoStatus } from "@/components/video-status";
import {
  createVideo,
  completeVideo,
  getVideo,
  type VideoWithRenditions,
} from "@/lib/api";

type Stage =
  | { kind: "form" }
  | { kind: "uploading"; percent: number }
  | { kind: "status"; video: VideoWithRenditions };

export default function UploadPage() {
  const router = useRouter();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [stage, setStage] = useState<Stage>({ kind: "form" });
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!file) return;
    setError(null);

    try {
      const { id, uploadUrl } = await createVideo({
        title,
        description: description || undefined,
      });

      setStage({ kind: "uploading", percent: 0 });
      await putFile(uploadUrl, file, (percent) =>
        setStage({ kind: "uploading", percent }),
      );

      await completeVideo(id);
      const video = await getVideo(id);
      if (!video) throw new Error("Video disappeared after upload");
      setStage({ kind: "status", video });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Upload failed");
      setStage({ kind: "form" });
    }
  }

  function handleReady(video: VideoWithRenditions) {
    toast.success("Video ready");
    router.push(`/watch/${video.id}`);
  }

  if (stage.kind === "status") {
    return (
      <div className="mx-auto flex max-w-md flex-col gap-4 px-4 py-16">
        <h1 className="text-xl font-semibold">{title}</h1>
        <VideoStatus video={stage.video} onReady={handleReady} />
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-md flex-col gap-4 px-4 py-16">
      <h1 className="text-xl font-semibold">Upload a video</h1>
      <form className="flex flex-col gap-4" onSubmit={handleSubmit}>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="title">Title</Label>
          <Input
            id="title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            required
            maxLength={200}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="description">Description</Label>
          <Textarea
            id="description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            maxLength={2000}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="file">Video file</Label>
          <Input
            id="file"
            type="file"
            accept="video/*"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            required
          />
        </div>
        {stage.kind === "uploading" ? (
          <div className="flex flex-col gap-1.5">
            <Progress value={stage.percent} />
            <p className="text-xs text-muted-foreground">
              {stage.percent}% uploaded
            </p>
          </div>
        ) : null}
        {error ? <p className="text-sm text-destructive">{error}</p> : null}
        <Button
          type="submit"
          disabled={!file || stage.kind === "uploading"}
        >
          {stage.kind === "uploading" ? "Uploading…" : "Upload"}
        </Button>
      </form>
    </div>
  );
}

function putFile(
  url: string,
  file: File,
  onProgress: (percent: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", url);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) {
        onProgress(Math.round((e.loaded / e.total) * 100));
      }
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
      } else {
        reject(new Error(`Upload failed with status ${xhr.status}`));
      }
    };
    xhr.onerror = () => reject(new Error("Upload failed"));
    xhr.send(file);
  });
}
