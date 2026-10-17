// APIPod OpenAPI and asynchronous task documentation, verified 2026-10-13:
// https://docs.apipod.ai/zh-CN/seedance/2-0-x-reference-to-video
// https://docs.apipod.ai/zh-CN/seedance/2-0-x-text-to-video
// Spicy resolution/audio fields verified against OpenAPI 2026-10-15:
// https://docs.apipod.ai/api-reference/openapi/seedance--2-0-spicy-text-to-video.zh.yaml
// https://docs.apipod.ai/api-reference/openapi/seedance--2-0-spicy-reference-to-video.zh.yaml
// https://docs.apipod.ai/api-reference/openapi/query-video-task.yaml
const MODELS = ["seedance-2.0-x-r2v", "seedance-2.0-x-t2v","seedance-2.0-spicy-r2v","seedance-2.0-spicy-t2v"];
const RATIOS = ["16:9", "9:16", "4:3", "3:4", "1:1", "21:9"];
const MEDIA = [
  { names: ["images", "image_urls"], limit: 9 },
  { names: ["videos", "video_urls"], limit: 3 },
  { names: ["audios", "audio_urls"], limit: 3 },
];
const MEDIA_FIELDS = MEDIA.flatMap(function (group) { return group.names; });
const FIELDS = ["model", "prompt", "seconds", "duration", "resolution", "size", "ratio", "aspect_ratio", "n", "generate_audio"].concat(MEDIA_FIELDS);

export const meta = {
  apiVersion: 1,
  key: "apipod",
  name: "APIPod",
  version: "1.0.4",
  author: { name: "jiayi-1994" },
  icon: "text:AP",
  description: { en: "Seedance video generation through the APIPod API", zh: "通过 APIPod 接口生成 Seedance 视频" },
  baseUrl: "https://api.apipod.ai",
  website: "https://docs.apipod.ai",
  models: MODELS,
  fetchMode: "per_task",
  protocols: ["openai_video"],
  // Both X models use a fixed per-video charge, independent of duration and
  // reference lengths. Actual prices belong in administrator configuration.
  usageSchema: {
    video_count: { type: "number", unit: "count", description: { en: "Video generation unit price", zh: "视频生成单价" } },
  },
  usageExamples: [{ label: "720p", facts: { video_count: 1 } }],
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function httpURL(value) {
  return typeof value === "string" && value.length <= 8192 && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value) && !/[\u0000-\u001f\u007f]/.test(value);
}

function duration(value) {
  // Vendor's 5–15 bound is stricter than relay/common.MaxTaskDurationSeconds.
  if ((typeof value !== "number" && typeof value !== "string") || !/^\d+$/.test(String(value))) throw new Error("duration must be an integer between 5 and 15 seconds");
  const seconds = Number(value);
  if (!Number.isInteger(seconds) || seconds < 5 || seconds > 15) throw new Error("duration must be an integer between 5 and 15 seconds");
  return seconds;
}

function resolution(value) {
  if (typeof value !== "string" || !["480p", "720p"].includes(value.toLowerCase())) throw new Error("resolution must be 480p or 720p");
  return value.toLowerCase();
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

// All surfaces share normalization, including overrides applied after decoding.
// Equal aliases denote the same list; repeats inside a list remain separate inputs.
function videoRequest(input) {
  if (!isObject(input)) throw new Error("video request must be an object");
  for (const name of Object.keys(input)) {
    if (name === "first_image_url" || name === "last_image_url") throw new Error("APIPod Seedance X does not support first or last frames");
    if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
  }
  if (has(input, "n") && input.n !== 1) throw new Error("n must be 1");
  if (typeof input.prompt !== "string" || !input.prompt.trim() || Array.from(input.prompt).length > 30000) throw new Error("prompt must contain 1 to 30000 characters");
  const seconds = scalarAlias(input, ["seconds", "duration"], duration);
  let tier = has(input, "resolution") ? resolution(input.resolution) : undefined;
  let aspect = scalarAlias(input, ["ratio", "aspect_ratio"], ratio);
  if (has(input, "size")) {
    if (typeof input.size !== "string") throw new Error("size must be 480p, 720p or WIDTHxHEIGHT");
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
  const result = { prompt: input.prompt, duration: seconds === undefined ? 5 : seconds, resolution: tier || "720p", aspect_ratio: aspect || "16:9" };
  if (has(input, "generate_audio")) {
    if (typeof input.generate_audio !== "boolean") throw new Error("generate_audio must be a boolean");
    result.generate_audio = input.generate_audio;
  }
  for (const group of MEDIA) {
    let selected;
    for (const name of group.names) {
      if (!has(input, name)) continue;
      const urls = input[name];
      if (!Array.isArray(urls) || urls.length > group.limit || urls.some(function (url) { return !httpURL(url); })) throw new Error(name + " must be an array of at most " + group.limit + " public HTTP(S) URLs without credentials");
      if (selected !== undefined && JSON.stringify(selected) !== JSON.stringify(urls)) throw new Error("conflicting reference fields: " + group.names.join("/"));
      selected = Array.from(urls);
    }
    if (selected && selected.length) result[group.names[1]] = selected;
  }
  return result;
}

function modelRequest(ctx) {
  const model = ctx.upstreamModel || ctx.model;
  if (!MODELS.includes(model)) throw new Error("unsupported APIPod model: " + String(model));
  const request = videoRequest(ctx.requestBody);
  if (model.startsWith("seedance-2.0-x-") && request.resolution !== "720p") throw new Error("APIPod Seedance X supports only 720p resolution");
  const visual = request.image_urls || request.video_urls;
  const references = visual || request.audio_urls;
  if (model.endsWith("-t2v") && references) throw new Error(model + " does not support reference media");
  if (model === "seedance-2.0-x-r2v" && !visual) throw new Error("seedance-2.0-x-r2v requires at least one image or video; audio cannot be used alone");
  if (model === "seedance-2.0-spicy-r2v" && !references) throw new Error("seedance-2.0-spicy-r2v requires at least one image, video or audio reference");
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
  const body = { model: normalized.model, ...normalized.request };
  // X accepts the canvas flag locally but has no upstream audio toggle.
  // Spicy supports it; preserve explicit false and leave omission unchanged.
  if (normalized.model.startsWith("seedance-2.0-x-")) delete body.generate_audio;
  return {
    url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos/generations", method: "POST",
    headers: { ...authHeaders(ctx), "Content-Type": "application/json", "Idempotency-Key": ctx.publicTaskId },
    body,
  };
}

export function extractUsage(ctx) {
  modelRequest(ctx);
  return { video_count: 1 };
}

export function extractUsageOnComplete() {
  // usage.cost is the provider's USD charge, not a quantity or host quota.
  // Keep the fixed per-video reservation; failed tasks use the host refund path.
  return {};
}

export function describeSpec(ctx) {
  const request = modelRequest(ctx).request;
  return {
    spec_version: 3, output_seconds: request.duration, seconds_kind: "exact", resolution: request.resolution,
    references: { image: (request.image_urls || []).length, video: (request.video_urls || []).length, audio: (request.audio_urls || []).length, frame: 0 },
    reference_video_urls: request.video_urls || [],
  };
}

function taskData(body) {
  return isObject(body) && body.code === 200 && !body.error && isObject(body.data) ? body.data : null;
}

function videoURL(body) {
  const data = taskData(body);
  return data && data.status === "completed" && !data.error && Array.isArray(data.result) && httpURL(data.result[0]) ? data.result[0] : "";
}

export function parseSubmitResponse(_ctx, response) {
  const data = taskData(response.body);
  // Undocumented 2xx errors or missing IDs are ambiguous: throwing prevents
  // automatic resubmission of work that might already have been accepted.
  if (!data || typeof data.task_id !== "string" || !data.task_id.trim() || data.task_id !== data.task_id.trim() || (data.error && !has(data, "status"))) throw new Error("APIPod create response has no unambiguous task id");
  const output = { taskId: data.task_id, taskData: response.body };
  if (has(data, "status")) {
    const result = parseTaskResult({ taskId: data.task_id }, response.body);
    if (result.status === "UNKNOWN") throw new Error(result.reason);
    if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  }
  return output;
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId) throw new Error("upstream task id is required");
  return { url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos/status/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function parseTaskResult(ctx, body) {
  const data = taskData(body);
  if (!data) return { status: "UNKNOWN", reason: "invalid APIPod task response" };
  if (typeof data.task_id !== "string" || !data.task_id.trim() || data.task_id !== ctx.taskId) return { status: "UNKNOWN", reason: "upstream task id does not match the queried task" };
  if (data.status === "failed" || data.status === "cancelled") {
    const detail = typeof data.error === "string" && data.error.trim() ? data.error : "video generation failed";
    return { status: "FAILURE", reason: (data.status === "cancelled" ? "apipod_task_cancelled: " : "") + detail };
  }
  if (data.error) return { status: "UNKNOWN", reason: "conflicting APIPod task response" };
  if (data.status === "completed" && videoURL(body)) return { status: "SUCCESS", progress: "100%" };
  const status = data.status === "pending" ? "QUEUED" : data.status === "processing" ? "IN_PROGRESS" : "UNKNOWN";
  if (status === "UNKNOWN") return { status: "UNKNOWN", reason: "unrecognized task status or missing video URL" };
  const result = { status };
  if (Number.isInteger(data.progress) && data.progress >= 0 && data.progress <= 100) result.progress = data.progress + "%";
  return result;
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && videoURL(task.data) ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  const url = videoURL(ctx.data);
  if (ctx.artifactKey !== "video" || !url) throw new Error("artifact_not_found");
  // Host enforces SSRF and forwards Range, without sending API keys to the CDN.
  return { url, method: ctx.clientRequest.method, credentialless: true };
}

export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (text.startsWith("apipod_task_cancelled:") || /cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  if (/timeout|timed out|unavailable|internal (server )?error|insufficient|quota|rate.limit|unauthorized|authentication|超时|余额|额度|限流|服务异常|服务繁忙/.test(text)) return "upstream";
  if (/content policy violation|content moderation|moderation (rejected|blocked)|invalid (image|video|audio|prompt|url)\b|审核(未通过|不通过|拒绝)|未通过(内容)?审核|内容.{0,12}(违规|敏感|不合规)/.test(text)) return "user";
  return "upstream";
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const body = ctx.body;
      let input;
      if (body && body.kind === "json") input = body.value;
      else if (body && body.kind === "multipart") {
        if ((body.files || []).length) throw new Error("APIPod requires public media URLs; local file uploads are unsupported");
        input = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
          if (has(input, name)) throw new Error("duplicate video parameter: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (MEDIA_FIELDS.includes(name)) {
            if (entries.length === 1 && entries[0].startsWith("[")) {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be a URL array"); }
            } else input[name] = entries;
          } else {
            if (entries.length !== 1) throw new Error(name + " must be provided once");
            if (name === "generate_audio") {
              if (entries[0] !== "true" && entries[0] !== "false") throw new Error("generate_audio must be a boolean");
              input[name] = entries[0] === "true";
            } else input[name] = name === "n" && entries[0] === "1" ? 1 : entries[0];
          }
        }
      } else throw new Error("JSON or multipart body required");
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      if (ctx.model && input.model !== ctx.model) throw new Error("model does not match the selected model");
      const requestBody = videoRequest(input);
      // Initial candidate decoding precedes channel mapping. Model-specific
      // constraints are checked once the executing upstream identity is known.
      if (ctx.upstreamModel) modelRequest({ model: input.model, upstreamModel: ctx.upstreamModel, requestBody });
      return { kind: "submit", model: input.model, action: requestBody.image_urls || requestBody.video_urls || requestBody.audio_urls ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      const url = task.status === "SUCCESS" ? videoURL(task.data) : "";
      return url ? { url } : {};
    },
  },
};
