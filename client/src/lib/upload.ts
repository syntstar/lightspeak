// Streaming attachment upload with progress (XHR: fetch has no upload
// progress). The server checks size/quota from Content-Length first.
import { ApiError, WEB_HEADERS } from "./api/client";
import type { Attachment, ID } from "./api/types";
import { api, app } from "./state/app.svelte";

export interface UploadHandle {
  promise: Promise<Attachment>;
  abort(): void;
}

export function upload(channelId: ID, file: File, onProgress: (fraction: number) => void): UploadHandle {
  const xhr = new XMLHttpRequest();
  const promise = new Promise<Attachment>((resolve, reject) => {
    if (file.size > app.limits.uploadBytes) {
      reject(new ApiError("file_too_large", 413));
      return;
    }
    xhr.open("POST", `${api.base}/api/v1/channels/${channelId}/attachments`);
    if (api.token) xhr.setRequestHeader("Authorization", `Bearer ${api.token}`);
    for (const [k, v] of Object.entries(WEB_HEADERS)) xhr.setRequestHeader(k, v);
    xhr.setRequestHeader("X-Filename", encodeURIComponent(file.name));
    xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(e.loaded / e.total);
    };
    xhr.onload = () => {
      let body: unknown;
      try {
        body = JSON.parse(xhr.responseText);
      } catch {
        body = undefined;
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(body as Attachment);
      else
        reject(
          new ApiError(
            (body as { error?: { code?: string } })?.error?.code ?? `http_${xhr.status}`,
            xhr.status,
          ),
        );
    };
    xhr.onerror = () => reject(new ApiError("network", 0));
    xhr.onabort = () => reject(new ApiError("aborted", 0));
    xhr.send(file);
  });
  return { promise, abort: () => xhr.abort() };
}
