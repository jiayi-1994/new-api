// Task Plugin API v1 for https://api.meaicc.com/create/sd-2.html.
// One accepted generation reserves one request; the New API host owns settlement/refunds.
const MODELS = [
  "mx-h3", "sd-2-c1", "sd-2-c3", "sd-2-c4", "sd-2-c5", "sd-2-c6",
  "sd-2-c7", "sd-2-c8", "sd-2-fast", "sd-2.5-c1", "w3-c1",
];
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"];
const MEDIA_TYPES = ["first_frame", "last_frame", "reference_image", "reference_voice", "reference_video"];
const REFERENCE_FIELDS = [
  "images", "image_urls", "referenceImages", "reference_images", "input_reference",
  "videos", "video_urls", "referenceVideos", "reference_videos",
  "audios", "audio_urls", "referenceAudios", "reference_audios",
];
const FLAT_FIELDS = ["model", "prompt", "media", "seconds", "duration", "resolution", "size", "ratio", "aspect_ratio", "aspectRatio"].concat(REFERENCE_FIELDS);

export const meta = {
  apiVersion: 1,
  key: "meaicc",
  name: "Meaicc Video",
  version: "1.1.0",
  author: { name: "jiayi-1994" },
  description: { en: "Video generation through Meaicc, billed per request", zh: "通过 Meaicc 生成视频，按次计费" },
  icon: "text:M",
  baseUrl: "https://api.meaicc.com",
  models: MODELS,
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: {
    requests: {
      type: "number", unit: "count",
      description: { en: "Video generation unit price", zh: "视频生成单价" },
    },
  },
  usageExamples: [{ label: "1 video", facts: { requests: 1 } }],
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

// Copy only recognized fields; never pass a client's option bag through to the vendor.
function knownFields(value, allowed, label) {
  if (!isObject(value)) throw new Error(label + " must be an object");
  const output = {};
  for (const key of allowed) {
    if (has(value, key)) output[key] = value[key];
  }
  return output;
}

function httpURL(value) {
  return typeof value === "string" && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value.trim());
}

function duration(value) {
  if ((typeof value !== "number" && typeof value !== "string") || !/^[1-9]\d*$/.test(String(value).trim())) {
    throw new Error("duration must be an integer number of seconds");
  }
  const seconds = Number(value);
  // All currently declared models have a ceiling of 30s, below the host's task duration limit.
  if (!Number.isSafeInteger(seconds) || seconds > 30) throw new Error("duration must be between 1 and 30 seconds");
  return seconds;
}

function resolution(value) {
  if (typeof value !== "string" || !["720p", "768p", "1080p"].includes(value.trim().toLowerCase())) {
    throw new Error("resolution must be 720p, 768p or 1080p");
  }
  return value.trim().toLowerCase();
}

function ratio(value) {
  if (typeof value !== "string" || !RATIOS.includes(value.trim())) throw new Error("ratio must be one of " + RATIOS.join(", "));
  return value.trim();
}

function scalarAlias(input, names, parse) {
  let selected;
  for (const name of names) {
    if (!has(input, name)) continue;
    const value = parse(input[name]);
    if (selected !== undefined && selected !== value) throw new Error(name + " conflicts with " + names[0]);
    selected = value;
  }
  return selected;
}

function mediaList(value) {
  if (!Array.isArray(value) || value.length > 20) throw new Error("media must be an array of at most 20 references");
  return value.map(function (item) {
    item = knownFields(item, ["type", "url"], "media item");
    if (!MEDIA_TYPES.includes(item.type)) throw new Error("unsupported media type: " + item.type);
    if (!httpURL(item.url)) throw new Error("media URLs must use http(s) without embedded credentials");
    return { type: item.type, url: item.url.trim() };
  });
}

function referenceURLs(value, field) {
  if (Array.isArray(value) && value.length === 1 && typeof value[0] === "string" && value[0].trim().startsWith("[")) value = value[0];
  if (typeof value === "string" && value.trim().startsWith("[")) {
    try { value = JSON.parse(value); } catch (_error) { throw new Error(field + " must be a URL or JSON URL array"); }
  }
  if (field === "input_reference" && isObject(value)) {
    value = knownFields(value, ["image_url"], "input_reference");
    value = value.image_url;
  }
  if (typeof value === "string") value = [value];
  if (!Array.isArray(value) || value.length > 20) throw new Error(field + " must be a URL or URL array of at most 20 items");
  return value.map(function (url) {
    if (!httpURL(url)) throw new Error(field + " entries must be http(s) URLs without embedded credentials");
    return url.trim();
  });
}

// Both supported client forms become the documented input/parameters contract.
// Driver and billing hooks revalidate it so sandbox/direct hook calls cannot bypass limits.
function videoRequest(value) {
  if (!isObject(value)) throw new Error("video request must be an object");
  let input;
  let parameters;
  if (has(value, "input") || has(value, "parameters")) {
    // Conflicting request forms remain an error; unrelated client options are ignored.
    for (const key of FLAT_FIELDS) {
      if (key !== "model" && has(value, key)) throw new Error("unsupported video request field: " + key + "; do not mix flat and nested parameters");
    }
    input = knownFields(value.input, ["prompt", "media"], "input");
    parameters = knownFields(value.parameters, ["duration", "seconds", "resolution", "ratio", "aspect_ratio", "aspectRatio"], "parameters");
  } else {
    value = knownFields(value, FLAT_FIELDS, "video request");
    input = { prompt: value.prompt };
    parameters = {};
    parameters.duration = scalarAlias(value, ["duration", "seconds"], duration);
    if (has(value, "resolution")) parameters.resolution = resolution(value.resolution);
    const selectedRatio = scalarAlias(value, ["ratio", "aspect_ratio", "aspectRatio"], ratio);
    if (selectedRatio !== undefined) parameters.ratio = selectedRatio;
    if (has(value, "size")) {
      if (typeof value.size !== "string") throw new Error("size must be a resolution or WIDTHxHEIGHT");
      const size = value.size.trim().toLowerCase();
      const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(size);
      let selectedResolution;
      let selectedRatio;
      if (match) {
        const width = Number(match[1]);
        const height = Number(match[2]);
        selectedResolution = resolution(Math.min(width, height) + "p");
        for (const name of RATIOS) {
          const parts = name.split(":");
          if (Math.abs(width / height - Number(parts[0]) / Number(parts[1])) < 0.01) selectedRatio = name;
        }
        if (!selectedRatio) throw new Error("size does not map to a supported ratio");
      } else {
        selectedResolution = resolution(size);
      }
      if (has(parameters, "resolution") && parameters.resolution !== selectedResolution) throw new Error("size conflicts with resolution");
      if (selectedRatio && has(parameters, "ratio") && parameters.ratio !== selectedRatio) throw new Error("size conflicts with ratio");
      parameters.resolution = selectedResolution;
      if (selectedRatio) parameters.ratio = selectedRatio;
    }
    const media = [];
    for (const group of [
      { fields: ["images", "image_urls", "referenceImages", "reference_images", "input_reference"], type: "reference_image" },
      { fields: ["videos", "video_urls", "referenceVideos", "reference_videos"], type: "reference_video" },
      { fields: ["audios", "audio_urls", "referenceAudios", "reference_audios"], type: "reference_voice" },
    ]) {
      const names = group.fields.filter(function (name) { return has(value, name); });
      if (!names.length) continue;
      if (has(value, "media")) throw new Error("media cannot be combined with reference URL fields");
      const urls = referenceURLs(value[names[0]], names[0]);
      for (const name of names.slice(1)) {
        const other = referenceURLs(value[name], name);
        // Order is meaningful to prompts such as @image1. Equal aliases must not
        // append a second copy; different lists must not be merged or dropped.
        if (other.length !== urls.length || other.some(function (url, index) { return url !== urls[index]; })) {
          throw new Error("conflicting reference fields: " + names.join(", "));
        }
      }
      for (const url of urls) media.push({ type: group.type, url });
    }
    if (has(value, "media")) input.media = value.media;
    else if (media.length) input.media = media;
  }
  if (typeof input.prompt !== "string" || !input.prompt.trim()) throw new Error("prompt is required");
  const output = {
    input: { prompt: input.prompt },
    parameters: { duration: duration(scalarAlias(parameters, ["duration", "seconds"], duration)), resolution: resolution(parameters.resolution) },
  };
  const selectedRatio = scalarAlias(parameters, ["ratio", "aspect_ratio", "aspectRatio"], ratio);
  if (selectedRatio !== undefined) output.parameters.ratio = selectedRatio;
  if (has(input, "media")) output.input.media = mediaList(input.media);
  return output;
}

function modelRequest(ctx) {
  const model = ctx.upstreamModel || ctx.model;
  if (!MODELS.includes(model)) throw new Error("unsupported upstream video model: " + model);
  const body = videoRequest(ctx.requestBody);
  const params = body.parameters;
  const min = model === "w3-c1" ? 5 : 4;
  const max = model === "w3-c1" || model === "sd-2.5-c1" ? 30 : 15;
  if (params.duration < min || params.duration > max) throw new Error(model + " duration must be between " + min + " and " + max + " seconds");
  if (model === "sd-2-fast" && params.duration !== 10) throw new Error("sd-2-fast only supports 10 seconds");
  if (model === "sd-2-c8" && ![10, 15].includes(params.duration)) throw new Error("sd-2-c8 only supports 10 or 15 seconds");
  if (model === "sd-2.5-c1" && params.duration !== 30) throw new Error("sd-2.5-c1 only supports 30 seconds");
  const resolutions = model === "mx-h3" ? ["768p"] : model === "w3-c1" ? ["720p", "1080p"] : ["720p"];
  if (!resolutions.includes(params.resolution)) throw new Error(model + " resolution must be " + resolutions.join(" or "));
  if (["sd-2-c4", "sd-2-c6"].includes(model) && has(params, "ratio") && !["1:1", "16:9", "9:16"].includes(params.ratio)) {
    throw new Error(model + " ratio must be 1:1, 16:9 or 9:16");
  }
  const counts = { first_frame: 0, last_frame: 0, reference_image: 0, reference_voice: 0, reference_video: 0 };
  for (const item of body.input.media || []) counts[item.type] += 1;
  const images = counts.first_frame + counts.last_frame + counts.reference_image;
  const maxImages = model === "w3-c1" || model === "sd-2.5-c1" ? 10 : 9;
  const imageOnly = model === "sd-2-fast" || model === "sd-2.5-c1";
  const maxOther = imageOnly ? 0 : model === "w3-c1" ? 5 : 3;
  if (images > maxImages || counts.reference_video > maxOther || counts.reference_voice > maxOther) {
    throw new Error(model + " allows at most " + maxImages + " images, " + maxOther + " videos and " + maxOther + " audio references");
  }
  if (counts.first_frame > 1 || counts.last_frame > 1) throw new Error("provide at most one first_frame and one last_frame");
  return { model, input: body.input, parameters: params };
}

function baseURL(ctx) {
  if (typeof ctx.baseUrl !== "string" || !/^https?:\/\/[^\s/?#@\\]+(?:\/[^\s?#\\]*)?$/i.test(ctx.baseUrl)) {
    throw new Error("channel Base URL must be an http(s) address without query, fragment or credentials");
  }
  return ctx.baseUrl.replace(/\/+$/, "");
}

function authHeaders(ctx) {
  const authorization = ctx.authHeader || (ctx.apiKey ? "Bearer " + ctx.apiKey : "");
  if (typeof authorization !== "string" || !authorization.trim()) throw new Error("channel API key is required");
  return { Authorization: authorization };
}

function taskResult(body) {
  if (!isObject(body)) return { status: "UNKNOWN", reason: "invalid task response" };
  const raw = typeof body.status === "string" ? body.status.trim() : "";
  const status = raw.toUpperCase();
  const statuses = {
    PENDING: "QUEUED", QUEUED: "QUEUED",
    RUNNING: "IN_PROGRESS", PROCESSING: "IN_PROGRESS", IN_PROGRESS: "IN_PROGRESS",
    SUCCEEDED: "SUCCESS", SUCCESS: "SUCCESS", COMPLETED: "SUCCESS",
    FAILED: "FAILURE", FAILURE: "FAILURE", CANCELLED: "FAILURE", CANCELED: "FAILURE",
  };
  const failed = /^FAILED\s*[:：]/i.test(raw);
  const mapped = failed ? "FAILURE" : has(statuses, status) ? statuses[status] : undefined;
  if (!mapped) return { status: "UNKNOWN", reason: "unrecognized video task status: " + raw };
  const result = { status: mapped };
  if (mapped === "FAILURE") {
    const reason = failed ? raw.replace(/^FAILED\s*[:：]\s*/i, "") : body.error || body.message;
    result.reason = typeof reason === "string" && reason ? reason : isObject(reason) && typeof reason.message === "string" ? reason.message : "video generation failed";
  }
  if (typeof body.progress === "number" && Number.isFinite(body.progress) && body.progress >= 0 && body.progress <= 100) result.progress = body.progress + "%";
  return result;
}

export function buildSubmitRequest(ctx) {
  return {
    url: baseURL(ctx) + "/v1/videos", method: "POST",
    headers: Object.assign(authHeaders(ctx), { "Content-Type": "application/json" }),
    body: modelRequest(ctx),
  };
}

export function extractUsage(ctx) {
  modelRequest(ctx);
  // No client/upstream value can change the billable count. No duration multiplier.
  return { requests: 1 };
}

export function extractUsageOnComplete() {
  return { requests: 1 };
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  if (!isObject(body)) throw new Error("upstream create response must be an object");
  const result = taskResult(body);
  if ((body.error || body.success === false) && result.status !== "FAILURE") throw new Error("upstream rejected video creation");
  const taskId = body.task_id || body.id;
  if (typeof taskId !== "string" || !taskId.trim()) throw new Error(result.reason || "upstream create response has no task id");
  const output = { taskId: taskId.trim(), taskData: body };
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId.trim()) throw new Error("upstream task id is required");
  return { url: baseURL(ctx) + "/v1/videos/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function parseTaskResult(_ctx, body) {
  return taskResult(body);
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  if (!isObject(ctx.data) || !httpURL(ctx.data.object)) throw new Error("completed Meaicc task has no downloadable video URL in object");
  // Never forward the channel key to an upstream-supplied CDN URL; the host applies its SSRF guard.
  return { url: ctx.data.object.trim(), method: ctx.clientRequest && ctx.clientRequest.method === "HEAD" ? "HEAD" : "GET", credentialless: true };
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const body = ctx.body;
      if (!body || !["json", "multipart"].includes(body.kind)) throw new Error("JSON or multipart text fields are required");
      let value = body.value;
      if (body.kind === "multipart") {
        if ((body.files || []).length) throw new Error("file uploads are not supported; send public media URLs");
        value = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (!["input", "parameters"].concat(FLAT_FIELDS).includes(name)) continue;
          if (has(value, name)) throw new Error("duplicate field: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (REFERENCE_FIELDS.includes(name)) { value[name] = entries; continue; }
          if (entries.length !== 1) throw new Error(name + " must be provided once");
          value[name] = entries[0];
          if (["input", "parameters", "media"].includes(name)) {
            try { value[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must contain valid JSON"); }
          }
        }
      }
      if (!isObject(value) || typeof value.model !== "string" || !value.model.trim()) throw new Error("model is required");
      const model = value.model.trim();
      if (ctx.model && model !== ctx.model) throw new Error("model does not match the selected channel model");
      const requestBody = videoRequest(value);
      // Initial decoding can precede channel mapping. Only a pinned upstream model
      // determines capabilities; the billing and submit hooks always validate it.
      if (ctx.upstreamModel) modelRequest({ model, upstreamModel: ctx.upstreamModel, requestBody });
      const media = requestBody.input.media || [];
      return { kind: "submit", model, action: media.length ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      if (!isObject(task.data)) return {};
      const output = {};
      if (Number.isSafeInteger(task.data.seconds) && task.data.seconds >= 0 && task.data.seconds <= 30) output.seconds = task.data.seconds;
      if (task.status === "SUCCESS" && httpURL(task.data.object)) {
        output.video_url = task.data.object.trim();
        output.url = output.video_url;
      }
      if (task.status === "FAILURE") output.error = { message: taskResult(task.data).reason || "video generation failed" };
      return output;
    },
  },
};
