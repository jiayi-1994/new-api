// FreeAI / Token6688 Task Plugin API v1.
// Protocol: https://freeai.token6688.com/zh-CN/api-docs/media#ep-videos_flat
// Capabilities checked against /v1/skills/models/{model} and /v1/logical-models
// on 2026-10-13. Defaults below follow param_schema.default (the display-only
// options[].is_default currently disagrees for promo/special).
// Prices belong to host configuration, never this plugin.
const MODELS = {
  "seedance-2-0-official": {
    seconds: [4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15], ratio: "16:9",
    tiers: { "标准": ["480p", "720p", "1080p", "4k"], fast: ["480p", "720p"], mini: ["480p", "720p"] },
  },
  "seedance-2-0-promo": {
    seconds: [4, 5, 6, 7, 8, 9, 10, 12, 15], ratio: "9:16",
    tiers: { "标准": ["480p", "720p", "1080p", "2k", "4k"], fast: ["480p", "720p", "1080p"], mini: ["480p", "720p", "4k"] },
  },
  "seedance-2-0-special": {
    seconds: [15], ratio: "9:16",
    tiers: { "高清": ["480p", "720p"], "快速": ["480p", "720p"], "标准": ["480p", "720p"] },
  },
};
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"];
const TIERS = ["480p", "720p", "1080p", "2k", "4k"];
const MODES = ["text-to-video", "reference", "first-frame", "first-last"];
const MEDIA = ["images", "videos", "audios"];
const FIELDS = ["model", "prompt", "seconds", "duration", "resolution", "size", "ratio", "aspect_ratio", "quality", "mode", "n", "count", "first_image_url", "last_image_url", "input_reference", "return_last_frame", "generate_audio", "lane_id", ...MEDIA];
const USAGE = {
  seconds: { type: "number", unit: "second", description: { en: "Output video unit price", zh: "输出视频单价" } },
  requests: { type: "number", unit: "count", description: { en: "Video generation unit price", zh: "视频生成单价" } },
  resolution: { enum: TIERS, description: { en: "Output video resolution", zh: "输出视频分辨率" } },
  quality: { enum: ["标准", "fast", "mini"], description: { en: "Model quality tier", zh: "模型质量档位" }, enumLabels: { "标准": { en: "Standard", zh: "标准" }, fast: { en: "Fast", zh: "Fast" }, mini: { en: "Mini", zh: "Mini" } } },
  mode: { enum: MODES, description: { en: "Video generation mode", zh: "视频生成模式" }, enumLabels: { "text-to-video": { en: "Text to video", zh: "文生视频" }, reference: { en: "Reference to video", zh: "参考生视频" }, "first-frame": { en: "First frame", zh: "首帧" }, "first-last": { en: "First and last frames", zh: "首尾帧" } } },
  has_video: { type: "boolean", description: { en: "Reference video present", zh: "存在参考视频" } },
};

export const meta = {
  apiVersion: 1, key: "token6688", name: "FreeAI Token6688", version: "1.0.4",
  author: { name: "jiayi-1994" }, icon: "text:FA",
  website: "https://freeai.token6688.com", baseUrl: "https://freeai.token6688.com",
  description: { en: "Seedance video generation through FreeAI Token6688", zh: "通过 FreeAI Token6688 生成 Seedance 视频" },
  models: Object.keys(MODELS), protocols: ["openai_video"], fetchMode: "per_task",
  usageSchema: { seconds: USAGE.seconds, resolution: USAGE.resolution, quality: USAGE.quality, mode: USAGE.mode, has_video: USAGE.has_video },
  usageProfiles: Object.keys(MODELS).map(function (model) {
    const special = model === "seedance-2-0-special";
    const schema = {
      resolution: { ...USAGE.resolution, enum: TIERS.filter(tier => Object.values(MODELS[model].tiers).some(tiers => tiers.includes(tier))) },
      quality: { ...USAGE.quality, enum: Object.keys(MODELS[model].tiers), enumLabels: special ? { "高清": { en: "Standard", zh: "官方满血" }, "快速": { en: "Fast", zh: "Fast" }, "标准": { en: "Mini", zh: "Mini" } } : USAGE.quality.enumLabels },
      mode: USAGE.mode, has_video: USAGE.has_video,
    };
    schema[special ? "requests" : "seconds"] = special ? USAGE.requests : USAGE.seconds;
    return { models: [model], schema };
  }),
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function httpURL(value) {
  return typeof value === "string" && value.length <= 8192 && /^https?:\/\/[^\s/?#@\\%]+(?:[/?#][^\s\\]*)?$/i.test(value) && !/[\x00-\x1f\x7f]/.test(value);
}

function duration(value) {
  if ((typeof value !== "number" && typeof value !== "string") || !/^[1-9]\d*$/.test(String(value))) throw new Error("duration must be an integer from 4 to 15 seconds");
  const seconds = Number(value);
  // Vendor's 15s cap is stricter than relay/common.MaxTaskDurationSeconds.
  if (!Number.isSafeInteger(seconds) || seconds < 4 || seconds > 15) throw new Error("duration must be an integer from 4 to 15 seconds");
  return seconds;
}

function resolution(value) {
  if (typeof value !== "string") throw new Error("resolution must be a string");
  const text = value.toLowerCase();
  const tier = text === "2160p" ? "4k" : text === "1440p" ? "2k" : text;
  if (!TIERS.includes(tier)) throw new Error("unsupported resolution: " + value);
  return tier;
}

function aspectRatio(value) {
  if (!RATIOS.includes(value) && value !== "adaptive") throw new Error("unsupported aspect ratio");
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

// Shared by candidate decoding, pricing, scheduling and submission.
// Normalize mirrors without mutating inputs or dropping duplicates.
function videoRequest(value, model, deferQuality) {
  if (!isObject(value)) throw new Error("video request must be an object");
  const input = {};
  for (const bag of [value, value.params, value.metadata]) {
    if (bag === undefined) continue;
    if (!isObject(bag)) throw new Error("params and metadata must be objects");
    for (const key of Object.keys(bag)) {
      if (bag === value && (key === "params" || key === "metadata")) continue;
      if (!FIELDS.includes(key) || (bag !== value && key === "model")) throw new Error("unsupported video parameter: " + key);
      let fieldValue = bag[key];
      if (key === "seconds" || key === "duration") fieldValue = duration(fieldValue);
      if (key === "resolution") fieldValue = resolution(fieldValue);
      if (has(input, key) && JSON.stringify(input[key]) !== JSON.stringify(fieldValue)) throw new Error("conflicting video parameter: " + key);
      input[key] = fieldValue;
    }
  }
  const profile = model === undefined ? undefined : MODELS[model];
  if (model !== undefined && !has(MODELS, model)) throw new Error("unsupported Token6688 model: " + model);
  if (typeof input.prompt !== "string" || !input.prompt.trim() || Array.from(input.prompt).length > 30000) throw new Error("prompt must contain 1 to 30000 characters");
  for (const key of ["n", "count"]) {
    if (has(input, key) && input[key] !== 1) throw new Error(key + " must be 1");
  }
  const output = { prompt: input.prompt };
  if (has(input, "generate_audio")) {
    if (typeof input.generate_audio !== "boolean") throw new Error("generate_audio must be a boolean");
    // Canvas sends this optional switch. Preserve explicit false on the wire;
    // do not invent a default or claim per-lane audio-toggle support.
    output.generate_audio = input.generate_audio;
  }
  if (has(input, "lane_id")) {
    // Opaque Token6688 routing identity from pricing channels[].lane. Preserve
    // the whole value, not the vendor/group or the unrelated channel_id.
    if (typeof input.lane_id !== "string" || !input.lane_id || /[\s\x00-\x1f\x7f]/.test(input.lane_id)) throw new Error("lane_id must be a non-empty lane identifier without whitespace");
    output.lane_id = input.lane_id;
  }
  let seconds = scalarAlias(input, ["seconds", "duration"], duration);
  if (seconds === undefined && profile) seconds = profile.seconds[0];
  if (seconds !== undefined) {
    if (profile && !profile.seconds.includes(seconds)) throw new Error(model + " duration must be one of " + profile.seconds.join(", "));
    output.duration = seconds;
  }
  let tier = scalarAlias(input, ["resolution"], resolution);
  let ratio = scalarAlias(input, ["ratio", "aspect_ratio"], aspectRatio);
  if (has(input, "size")) {
    const match = typeof input.size === "string" && /^(\d{3,4})x(\d{3,4})$/.exec(input.size);
    let sizeTier;
    if (match) {
      const width = Number(match[1]), height = Number(match[2]);
      sizeTier = resolution(Math.min(width, height) + "p");
      const sizeRatio = RATIOS.find(function (candidate) {
        const parts = candidate.split(":");
        return Math.abs(width / height - Number(parts[0]) / Number(parts[1])) < 0.01;
      });
      if (!sizeRatio || (ratio !== undefined && ratio !== sizeRatio)) throw new Error("size conflicts with aspect ratio");
      ratio = sizeRatio;
    } else sizeTier = resolution(input.size);
    if (tier !== undefined && tier !== sizeTier) throw new Error("size conflicts with resolution");
    tier = sizeTier;
  }
  if (tier !== undefined || profile) output.resolution = tier || "480p";
  if (ratio !== undefined || profile) output.aspect_ratio = ratio || profile.ratio;
  // This deployment always uses official's standard quality. Keep the same
  // value in decoding, spec, usage and submission without host overrides.
  if (model === "seedance-2-0-official") {
    output.quality = "标准";
  } else if (has(input, "quality")) {
    if (typeof input.quality !== "string") throw new Error("quality must be a model quality tier");
    output.quality = input.quality;
  } else if (profile) output.quality = model === "seedance-2-0-special" ? "标准" : "mini";
  if (!deferQuality && profile && (!has(profile.tiers, output.quality) || !profile.tiers[output.quality].includes(output.resolution))) {
    throw new Error("unsupported quality/resolution combination for " + model);
  }
  for (const field of MEDIA) {
    const urls = has(input, field) ? input[field] : [];
    const limit = field === "images" ? 9 : 3;
    if (!Array.isArray(urls) || urls.length > limit || urls.some(url => !httpURL(url))) throw new Error(field + " must be an HTTP(S) URL array with at most " + limit + " items");
    if (urls.length) output[field] = Array.from(urls);
  }
  let first = input.first_image_url;
  if (has(input, "input_reference")) {
    const ref = input.input_reference;
    const url = isObject(ref) && Object.keys(ref).length === 1 ? ref.image_url : ref;
    if (!httpURL(url) || (first !== undefined && first !== url)) throw new Error("input_reference conflicts with first_image_url or is not an HTTP(S) URL");
    first = url;
  }
  const last = input.last_image_url;
  if ((has(input, "first_image_url") && !httpURL(first)) || (has(input, "last_image_url") && !httpURL(last))) throw new Error("frame fields must be HTTP(S) URLs");
  if (last !== undefined && first === undefined) throw new Error("last_image_url requires first_image_url");
  const frameMode = first !== undefined ? (last !== undefined ? "first-last" : "first-frame") : undefined;
  if (frameMode) {
    if (input.mode !== undefined && input.mode !== frameMode) throw new Error("mode conflicts with frame fields");
    const frames = last !== undefined ? [first, last] : [first];
    if (output.images && JSON.stringify(output.images) !== JSON.stringify(frames)) throw new Error("frame mode cannot include reference images");
    output.images = frames;
  }
  output.mode = input.mode === undefined ? frameMode || (MEDIA.some(field => output[field]) ? "reference" : "text-to-video") : input.mode;
  // edit chooses duration from input clips; extend returns input + new seconds.
  // Neither can truthfully describe an exact requested output for unified sales.
  if (!MODES.includes(output.mode)) throw new Error("unsupported mode; edit and extend do not preserve exact output duration");
  const images = (output.images || []).length, videos = (output.videos || []).length, audios = (output.audios || []).length;
  if (output.mode === "text-to-video" && images + videos + audios) throw new Error("text-to-video cannot include reference media");
  if (output.mode === "reference" && !images && !videos) throw new Error("reference mode requires an image or video");
  if (output.mode === "first-frame" || output.mode === "first-last") {
    const count = output.mode === "first-last" ? 2 : 1;
    if (images !== count || videos || audios) throw new Error(output.mode + " requires exactly " + count + " frame images and no other reference media");
  }
  if (model === "seedance-2-0-official" && audios && !images) throw new Error("official audio references require a reference image");
  if (!deferQuality && model === "seedance-2-0-special" && ((output.quality === "标准" && output.mode === "first-last") || (output.quality === "高清" && output.mode === "first-frame"))) throw new Error("unsupported special quality/mode combination");
  if (output.aspect_ratio === "adaptive") {
    if (!images || (model === "seedance-2-0-special") || (model === "seedance-2-0-promo" && ((!deferQuality && output.quality !== "mini") || output.mode === "first-last"))) throw new Error("adaptive ratio is not supported for these media, model, quality or mode");
  }
  if (has(input, "return_last_frame")) {
    if (typeof input.return_last_frame !== "boolean") throw new Error("return_last_frame must be a boolean");
    if (profile && model !== "seedance-2-0-official") throw new Error("return_last_frame is supported only by official");
    output.return_last_frame = input.return_last_frame;
  }
  return output;
}

function modelRequest(ctx) {
  const model = ctx.upstreamModel || ctx.model;
  if (!has(MODELS, model)) throw new Error("unsupported Token6688 model: " + model);
  return { model, ...videoRequest(ctx.requestBody, model) };
}

function authHeaders(ctx) {
  const key = ctx.authHeader || ctx.apiKey;
  if (typeof key !== "string" || !key.trim() || /[\r\n]/.test(key)) throw new Error("channel API key is required");
  return { Authorization: key.startsWith("Bearer ") ? key : "Bearer " + key };
}

export function buildSubmitRequest(ctx) {
  const body = modelRequest(ctx);
  if (typeof ctx.publicTaskId !== "string" || !ctx.publicTaskId || ctx.publicTaskId.length > 128) throw new Error("host public task id is required for idempotent submission");
  body.client_request_id = ctx.publicTaskId;
  return { url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos/generations", method: "POST", headers: { ...authHeaders(ctx), "Content-Type": "application/json" }, body };
}

export function extractUsage(ctx) {
  const body = modelRequest(ctx);
  const usage = { resolution: body.resolution, quality: body.quality, mode: body.mode, has_video: !!body.videos };
  usage[body.model === "seedance-2-0-special" ? "requests" : "seconds"] = body.model === "seedance-2-0-special" ? 1 : body.duration;
  return usage;
}

export function extractUsageOnComplete() {
  // duration_seconds is elapsed job time, NOT video length. actual_cost_micro_usd
  // is vendor money, NOT seconds/tokens. Retain the reserved output facts.
  // Input-video duration pricing belongs to the host's reference-media probe.
  return {};
}

export function describeSpec(ctx) {
  const body = modelRequest(ctx);
  const frames = body.mode === "first-frame" || body.mode === "first-last";
  return {
    spec_version: 3, output_seconds: body.duration,
    seconds_kind: body.model === "seedance-2-0-special" ? "fixed" : "exact",
    resolution: body.resolution,
    references: { image: frames ? 0 : (body.images || []).length, video: (body.videos || []).length, audio: (body.audios || []).length, frame: frames ? body.images.length : 0 },
    reference_video_urls: body.videos || [],
  };
}

function taskID(body) {
  if (!isObject(body)) return "";
  const id = body.task_id === undefined ? body.id : body.task_id;
  if (typeof id !== "string" || !id.trim() || id !== id.trim() || id.length > 256 || /[\x00-\x1f\x7f]/.test(id)) return "";
  if (body.id !== undefined && body.id !== id) return "";
  return id;
}

function videoURL(body) {
  if (!isObject(body) || body.is_final !== true || body.status !== "completed" || body.error) return "";
  // output_url is authoritative. Do not hide an invalid primary URL by falling
  // back to a different artifact. result_url is the documented legacy alias.
  const url = body.output_url === undefined || body.output_url === "" ? body.result_url : body.output_url;
  return httpURL(url) ? url : "";
}

export function parseTaskResult(ctx, body) {
  if (!taskID(body) || (ctx.taskId && taskID(body) !== ctx.taskId)) return { status: "UNKNOWN", reason: "invalid or mismatched Token6688 task id" };
  if (typeof body.is_final !== "boolean") return { status: "UNKNOWN", reason: "task response is missing is_final" };
  if (body.is_final && body.status === "failed") {
    const reason = typeof body.error === "string" ? body.error : isObject(body.error) && typeof body.error.message === "string" ? body.error.message : "video generation failed";
    return { status: "FAILURE", reason: reason || "video generation failed" };
  }
  if (body.is_final && body.status === "completed" && videoURL(body)) return { status: "SUCCESS", progress: "100%" };
  if (!body.is_final && !body.error && ["pending", "processing", "needs_review"].includes(body.status)) {
    const result = { status: body.status === "pending" ? "QUEUED" : "IN_PROGRESS" };
    if (body.progress_available === true && typeof body.progress === "number" && Number.isFinite(body.progress) && body.progress >= 0 && body.progress <= 100) result.progress = body.progress + "%";
    return result;
  }
  return { status: "UNKNOWN", reason: "unrecognized or inconsistent Token6688 task status or missing video URL" };
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  const id = taskID(body);
  // Missing IDs or undocumented 2xx errors are ambiguous, not a safe retry.
  if (!id || (body.error && !(body.is_final === true && body.status === "failed"))) throw new Error("create response has no unambiguous Token6688 task id");
  const output = { taskId: id, taskData: body };
  if (body.is_final === true) {
    const result = parseTaskResult({ taskId: id }, body);
    if (result.status === "UNKNOWN") throw new Error(result.reason);
    output.immediate = result;
  }
  return output;
}

export function buildQueryRequest(ctx) {
  if (!taskID({ id: ctx.taskId })) throw new Error("upstream task id is required");
  return { url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/tasks/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && videoURL(task.data) ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  const url = videoURL(ctx.data);
  if (ctx.artifactKey !== "video" || !url) throw new Error("artifact_not_found");
  // Host performs SSRF/redirect checks and forwards Range without credentials.
  return { url, method: ctx.clientRequest.method, credentialless: true };
}

export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (/timeout|timed out|unavailable|internal (server )?error|insufficient|quota|rate.limit|unauthorized|authentication|超时|余额|额度|限流|服务异常|服务繁忙/.test(text)) return "upstream";
  if (/cancell?ed by (the )?user\b/.test(text)) return "cancelled";
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
        if ((body.files || []).length) throw new Error("use public media URLs; local file uploads are unsupported");
        input = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (has(input, name)) throw new Error("duplicate video parameter: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (MEDIA.includes(name)) {
            if (entries.length === 1 && entries[0].startsWith("[")) {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be a URL array"); }
            } else input[name] = entries;
          } else {
            if (entries.length !== 1) throw new Error(name + " must be provided once");
            input[name] = entries[0];
            if (["n", "count", "return_last_frame", "generate_audio", "params", "metadata"].includes(name)) {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be valid JSON"); }
            }
          }
        }
      } else throw new Error("JSON or multipart body required");
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      if (ctx.model && input.model !== ctx.model) throw new Error("model does not match the selected model");
      const selected = ctx.upstreamModel || input.model;
      // Unknown public aliases must survive initial decoding before mapping.
      const model = ctx.upstreamModel || has(MODELS, selected) ? selected : undefined;
      // Quality may be selected by channel parameter overrides after decoding.
      // Every spec, usage and submit hook validates the effective combination.
      const requestBody = videoRequest(input, model, true);
      return { kind: "submit", model: input.model, action: requestBody.mode === "text-to-video" ? "text_to_video" : "image_to_video", requestBody };
    },
    render: function (_ctx, task) {
      const url = task.status === "SUCCESS" ? videoURL(task.data) : "";
      return url ? { url } : {};
    },
  },
};
