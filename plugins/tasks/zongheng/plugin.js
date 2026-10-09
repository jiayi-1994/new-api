// Task Plugin API v1 for the Zongheng public video API.
// Public model IDs verified through /v1/models on 2026-10-07. That endpoint
// does not expose capabilities or prices: configure model limits and purchase
// costs on each channel from the capability hub, never infer them from IDs.
const MODELS = [
  "XXseadanco2.0M", "XXseedacn2.5", "XXmini", "Xseadanco2.0", "Cseadanco2.5K",
  "Xminimex-h3", "wan-1080", "A-SD2.0", "sedanco2.0", "HHsd2.5",
  "Fseadanco2.5-480", "Fseadanco2.5-720", "Fsd2.5", "TTP-grok", "Fmini",
  "Fseadanco2.0mini", "00Psd2.5", "OOsd2.5", "00sd2.0", "Msedanco2.5-PRO", "Fmini2",
];
// Host safety bounds, not provider capability claims.
const MAX_TASK_DURATION_SECONDS = 3600; // relay/common.MaxTaskDurationSeconds
const MAX_REFERENCES = 1024; // pkg/videosched/spec.MaxReferenceCount
const RESOLUTIONS = ["480p", "720p", "768p", "1080p", "1440p", "4k"];
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"];
const MEDIA_GROUPS = [
  ["images", "reference_images", "referenceImages", "image_urls", "image", "input_reference"],
  ["reference_videos", "referenceVideos", "videos", "video_urls"],
  ["reference_audios", "referenceAudios", "audios", "audio_urls"],
];
const MEDIA_FIELDS = MEDIA_GROUPS.flat();
const FIELDS = ["model", "prompt", "seconds", "duration", "resolution", "size", "ratio", "aspect_ratio", "aspectRatio", "quality", "negative_prompt", "start_frame", "end_frame", "generate_audio", "n"].concat(MEDIA_FIELDS);

export const meta = {
  apiVersion: 1,
  key: "zongheng",
  name: "Zongheng Video",
  version: "1.0.1",
  author: { name: "jiayi-1994" },
  description: { en: "Video generation through the Zongheng API", zh: "通过纵横科技 API 生成视频" },
  icon: "text:纵横",
  baseUrl: "https://cnd-coo-new.pages.dev",
  website: "https://cnd-coo-new.pages.dev",
  models: MODELS,
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: {
    seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
    resolution: { enum: RESOLUTIONS, description: { en: "Output video resolution", zh: "输出视频分辨率" } },
  },
  usageExamples: [{ label: "720p · 10s", facts: { seconds: 10, resolution: "720p" } }],
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function httpsURL(value) {
  return typeof value === "string" && value.length <= 8192 && /^https:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value) && !/[\u0000-\u001f\u007f]/.test(value);
}

function duration(value) {
  if ((typeof value !== "number" && typeof value !== "string") || !/^\d+(?:\.\d+)?$/.test(String(value))) throw new Error("duration must be a positive number of seconds");
  const seconds = Number(value);
  if (!Number.isFinite(seconds) || seconds <= 0 || seconds > MAX_TASK_DURATION_SECONDS) throw new Error("duration must be greater than 0 and at most " + MAX_TASK_DURATION_SECONDS);
  return seconds;
}

function resolution(value) {
  if (typeof value !== "string") throw new Error("resolution must be a string");
  const tier = value.toLowerCase() === "2160p" ? "4k" : value.toLowerCase();
  if (!RESOLUTIONS.includes(tier)) throw new Error("unsupported resolution: " + value);
  return tier;
}

function ratio(value) {
  if (!RATIOS.includes(value)) throw new Error("unsupported aspect ratio: " + String(value));
  return value;
}

function scalarAlias(input, names, parse) {
  let selected;
  for (const name of names) {
    if (!has(input, name)) continue;
    const value = parse(input[name]);
    if (selected !== undefined && selected !== value) throw new Error("conflicting values for " + names.join("/"));
    selected = value;
  }
  return selected;
}

function mediaURLs(value) {
  if (Array.isArray(value) && value.length === 1 && typeof value[0] === "string" && value[0].startsWith("[")) value = value[0];
  if (typeof value === "string") {
    if (value.startsWith("[")) {
      try { value = JSON.parse(value); } catch (_error) { throw new Error("references must be a URL array"); }
    } else value = [value];
  }
  if (!Array.isArray(value) || value.length > MAX_REFERENCES || value.some(url => !httpsURL(url))) throw new Error("references must contain at most " + MAX_REFERENCES + " public HTTPS URLs without credentials");
  return Array.from(value);
}

// Decode, reservation, scheduling and forwarding all validate the same body,
// including any channel parameter overrides applied after initial decoding.
function videoRequest(input) {
  if (!isObject(input)) throw new Error("video request must be an object");
  for (const name of Object.keys(input)) if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
  if (has(input, "n") && input.n !== 1 && input.n !== "1") throw new Error("n must be 1");
  if (typeof input.prompt !== "string" || !input.prompt.trim()) throw new Error("prompt is required");
  const seconds = scalarAlias(input, ["duration", "seconds"], duration);
  if (seconds === undefined) throw new Error("duration or seconds is required");
  let tier = has(input, "resolution") ? resolution(input.resolution) : undefined;
  let aspect = scalarAlias(input, ["ratio", "aspect_ratio", "aspectRatio"], ratio);
  if (has(input, "size")) {
    if (typeof input.size !== "string") throw new Error("size must be a resolution or WIDTHxHEIGHT");
    const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(input.size);
    let sizeTier;
    if (match) {
      const width = Number(match[1]);
      const height = Number(match[2]);
      sizeTier = resolution(Math.min(width, height) + "p");
      const sizeRatio = RATIOS.find(function (value) {
        const parts = value.split(":");
        return Math.abs(width / height - Number(parts[0]) / Number(parts[1])) < 0.01;
      });
      if (!sizeRatio) throw new Error("size has an unsupported aspect ratio");
      if (aspect !== undefined && aspect !== sizeRatio) throw new Error("size conflicts with ratio");
      aspect = sizeRatio;
    } else sizeTier = resolution(input.size);
    if (tier !== undefined && tier !== sizeTier) throw new Error("size conflicts with resolution");
    tier = sizeTier;
  }
  if (tier === undefined) throw new Error("resolution or size is required");
  const result = { prompt: input.prompt, duration: seconds, resolution: tier };
  if (aspect !== undefined) result.ratio = aspect;
  for (const name of ["quality", "negative_prompt"]) {
    if (!has(input, name)) continue;
    if (typeof input[name] !== "string" || (name === "quality" && (!input[name].trim() || input[name].length > 128))) throw new Error(name + " must be a valid string");
    result[name] = input[name];
  }
  if (has(input, "generate_audio")) {
    if (typeof input.generate_audio !== "boolean") throw new Error("generate_audio must be a boolean");
    result.generate_audio = input.generate_audio;
  }
  for (const names of MEDIA_GROUPS) {
    let selected;
    for (const name of names) {
      if (!has(input, name)) continue;
      const urls = mediaURLs(input[name]);
      // Canvas clients may repeat the same reference list using several aliases.
      // Preserve repeats within a list, and never charge mirrored lists twice.
      if (selected !== undefined && JSON.stringify(selected) !== JSON.stringify(urls)) throw new Error("conflicting reference fields: " + names.join("/"));
      selected = urls;
    }
    if (selected && selected.length) result[names[0]] = selected;
  }
  for (const name of ["start_frame", "end_frame"]) {
    if (!has(input, name)) continue;
    if (!httpsURL(input[name])) throw new Error(name + " must be a public HTTPS URL without credentials");
    result[name] = input[name];
  }
  if (result.end_frame && !result.start_frame) throw new Error("end_frame requires start_frame");
  const imageCount = (result.images || []).length + Number(Boolean(result.start_frame)) + Number(Boolean(result.end_frame));
  if (imageCount > MAX_REFERENCES) throw new Error("too many image references");
  return result;
}

function modelRequest(ctx) {
  const model = ctx.upstreamModel || ctx.model;
  if (!MODELS.includes(model)) throw new Error("unsupported Zongheng public model: " + String(model));
  const request = videoRequest(ctx.requestBody);
  // The public documentation explicitly excludes audio/video references for Grok.
  if (model === "TTP-grok" && (request.reference_videos || request.reference_audios)) throw new Error("Grok does not support reference videos or audios");
  return { model, request };
}

function authHeaders(ctx) {
  const credential = ctx.authHeader || ctx.apiKey;
  if (typeof credential !== "string" || !credential.trim()) throw new Error("channel API key is required");
  const token = credential.trim().replace(/^Bearer\s+/i, "");
  if (!token || /\s/.test(token)) throw new Error("invalid channel API key");
  return { Authorization: "Bearer " + token };
}

export function buildSubmitRequest(ctx) {
  const normalized = modelRequest(ctx);
  if (typeof ctx.publicTaskId !== "string" || !ctx.publicTaskId) throw new Error("public task id is required for idempotent submission");
  return {
    url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos", method: "POST",
    headers: { ...authHeaders(ctx), "Content-Type": "application/json", "Idempotency-Key": ctx.publicTaskId },
    body: { model: normalized.model, ...normalized.request },
  };
}

export function extractUsage(ctx) {
  const request = modelRequest(ctx).request;
  return { seconds: request.duration, resolution: request.resolution };
}

export function extractUsageOnComplete() {
  // Queries return no measured duration/usage. Preserve the validated reservation.
  // Vendor credits are not seconds or gateway quota. Unified sales are host-owned.
  return {};
}

export function describeSpec(ctx) {
  const request = modelRequest(ctx).request;
  return {
    spec_version: 3, output_seconds: request.duration, seconds_kind: "exact", resolution: request.resolution,
    references: {
      image: (request.images || []).length,
      video: (request.reference_videos || []).length, audio: (request.reference_audios || []).length,
      frame: Number(Boolean(request.start_frame)) + Number(Boolean(request.end_frame)),
    },
    reference_video_urls: request.reference_videos || [],
  };
}

function videoURL(body) {
  if (!isObject(body) || body.status !== "succeeded") return "";
  for (const name of ["video_url", "url", "result_url"]) if (httpsURL(body[name])) return body[name];
  return "";
}

function failureReason(body) {
  const error = isObject(body.error) ? body.error : {};
  const code = typeof body.error_code === "string" ? body.error_code : typeof error.type === "string" ? error.type : "";
  const detail = typeof body.error_detail === "string" && body.error_detail.trim() ? body.error_detail : typeof error.message === "string" && error.message.trim() ? error.message : "video generation failed";
  return (code ? code + ": " : "") + detail;
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body)) return { status: "UNKNOWN", reason: "invalid Zongheng task response" };
  if (typeof body.task_id !== "string" || !body.task_id || (ctx.taskId && body.task_id !== ctx.taskId)) return { status: "UNKNOWN", reason: "upstream task id does not match the queried task" };
  if (body.status === "failed") return { status: "FAILURE", reason: failureReason(body) };
  if ((has(body, "code") && body.code !== 0) || body.success === false || body.error) return { status: "UNKNOWN", reason: "conflicting Zongheng task response" };
  if (body.status === "processing") return { status: "IN_PROGRESS" };
  if (body.status === "succeeded" && videoURL(body)) return { status: "SUCCESS", progress: "100%" };
  return { status: "UNKNOWN", reason: "unrecognized task status or missing video URL" };
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  if (!isObject(body)) throw new Error("invalid Zongheng create response");
  // Non-2xx errors are handled by the host. An undocumented 2xx error without
  // a task ID is ambiguous and must never trigger another billable submission.
  if (typeof body.task_id !== "string" || !body.task_id.trim() || body.task_id !== body.task_id.trim()) throw new Error("upstream create response has no valid task id");
  const result = parseTaskResult({ taskId: body.task_id }, body);
  if (result.status === "UNKNOWN") throw new Error(result.reason);
  const output = { taskId: body.task_id, taskData: body };
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId) throw new Error("upstream task id is required");
  // Poll timing belongs to the host; API v1 has no next_poll_seconds hook.
  return { url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/tasks/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && videoURL(task.data) ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  const url = videoURL(ctx.data);
  if (ctx.artifactKey !== "video" || !url) throw new Error("artifact_not_found");
  // Keep the returned URL intact. The host forwards Range and enforces SSRF;
  // the public content endpoint must receive neither client nor channel keys.
  return { url, method: ctx.clientRequest.method, credentialless: true };
}

export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (/timeout|timed out|unavailable|internal (server )?error|insufficient|quota|rate.limit|unauthorized|authentication|超时|余额|额度|限流|服务异常|服务繁忙/.test(text)) return "upstream";
  if (/cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  if (/^invalid_(prompt|image|video|audio|reference|parameter|url):|content policy violation|content moderation|moderation (rejected|blocked)|invalid (image|video|audio|prompt|url)\b|审核(未通过|不通过|拒绝)|未通过(内容)?审核|内容.{0,12}(违规|敏感|不合规)|参考图无效/.test(text)) return "user";
  return "upstream";
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const body = ctx.body;
      let input;
      if (body && body.kind === "json") input = body.value;
      else if (body && body.kind === "multipart") {
        if ((body.files || []).length) throw new Error("upload local files to the Zongheng /v1/media API first and send data[].url");
        input = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
          if (has(input, name)) throw new Error("duplicate video parameter: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (MEDIA_FIELDS.includes(name)) input[name] = entries;
          else {
            if (entries.length !== 1) throw new Error(name + " must be provided once");
            input[name] = entries[0];
            if (name === "generate_audio") {
              if (entries[0] !== "true" && entries[0] !== "false") throw new Error("generate_audio must be a boolean");
              input[name] = entries[0] === "true";
            }
          }
        }
      } else throw new Error("JSON or multipart body required");
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      if (ctx.model && input.model !== ctx.model) throw new Error("model does not match the selected model");
      const requestBody = videoRequest(input);
      // Candidate decoding can precede channel mapping, so validate model-specific
      // rules only after a mapped upstream model is available.
      if (ctx.upstreamModel) modelRequest({ model: input.model, upstreamModel: ctx.upstreamModel, requestBody });
      const references = requestBody.images || requestBody.start_frame || requestBody.reference_videos || requestBody.reference_audios;
      return { kind: "submit", model: input.model, action: references ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      const url = task.status === "SUCCESS" ? videoURL(task.data) : "";
      return url ? { url } : {};
    },
  },
};
