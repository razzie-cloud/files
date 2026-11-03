"use strict";
(() => {
  if (window.filesCast) return;
  const panel = document.querySelector("#cast-controller");
  if (!panel) return;
  const status = panel.querySelector("[data-cast-status]");
  let context, player, remote, sdkRequested = false;
  const controls = panel.querySelector("[data-cast-controls]");
  const photos = panel.querySelector("[data-cast-photos]");
  const videos = panel.querySelector("[data-cast-video]");
  const api = {
    async create(source) {
      const response = await fetch(source.endpoint, { method: "POST", credentials: "same-origin", redirect: "error",
        headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
        body: new URLSearchParams({ path: source.path, mode: source.mode }) });
      if (!response.ok) throw { safeMessage: response.status === 422 ? "This folder has no pictures, or the selected path is invalid." : "Unable to prepare playback. Check access, file format, and storage availability." };
      return response.json();
    },
    async remove(endpoint) {
      const response = await fetch(endpoint, { method: "POST", credentials: "same-origin", redirect: "error", headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" }, body: "" });
      if (!response.ok && response.status !== 404) throw new Error("cleanup failed");
    }
  };
  const owns = op => {
    const session = context?.getCurrentSession();
    return session && session === op.session && op.sent?.has(session.getMediaSession()?.media?.contentId);
  };
  const adapter = {
    pick() {
      if (!context) throw new Error("SDK unavailable");
      return context.requestSession().then(() => context.getCurrentSession(), () => { throw { cancelled: true }; });
    },
    async load(op, entry) {
      // Detect authorization/expiry failures directly; Cast LOAD errors cannot
      // reliably distinguish them from unsupported codecs. Never send cookies.
      const response = await fetch(entry.url, { method: "HEAD", credentials: "omit", redirect: "error", cache: "no-store" });
      if (!response.ok) throw { authorization: [401, 403, 404, 410].includes(response.status) };
      if (controller.operation !== op || op.disconnected || context.getCurrentSession() !== op.session) throw new Error("stale session");
      const info = new chrome.cast.media.MediaInfo(entry.url, entry.mime);
      info.streamType = op.data.mode === "slideshow" ? chrome.cast.media.StreamType.OTHER : chrome.cast.media.StreamType.BUFFERED;
      info.metadata = op.data.mode === "slideshow" ? new chrome.cast.media.PhotoMediaMetadata() : new chrome.cast.media.MovieMediaMetadata();
      info.metadata.title = entry.name;
      const request = new chrome.cast.media.LoadRequest(info);
      request.autoplay = true;
      op.sent ||= new Set();
      op.sent.add(entry.url);
      await op.session.loadMedia(request);
    },
    stop(op) {
      if (!owns(op)) return;
      const media = op.session.getMediaSession();
      return new Promise((resolve, reject) => media.stop(new chrome.cast.media.StopRequest(), resolve, reject));
    },
    toggle(op) { if (owns(op)) remote.playOrPause(); },
    reconnect(op) {
      const session = context?.getCurrentSession();
      if (!session || session.getSessionId() !== op.session.getSessionId()) return false;
      op.session = session;
      return true;
    }
  };
  const controller = new FilesCastController(adapter, api, { now: () => Date.now(), setTimeout: (fn, delay) => window.setTimeout(fn, delay), clearTimeout: id => window.clearTimeout(id) }, state => {
    panel.hidden = !state.message && !state.operation;
    status.textContent = state.message || "";
    const op = state.operation;
    controls.hidden = !op;
    panel.querySelector("[data-cast-toggle]").hidden = !op?.data;
    photos.hidden = op?.data?.mode !== "slideshow";
    videos.hidden = op?.data?.mode !== "video";
    panel.querySelector("[data-cast-toggle]").textContent = op?.data?.mode === "slideshow" ? (op.playing ? "Pause" : "Resume") : (player?.isPaused ? "Play" : "Pause");
    panel.querySelector("[data-cast-expiry]").textContent = op?.data ? `Access expires ${new Date(op.data.expires).toLocaleString()}.` : "";
    panel.querySelector("[data-cast-interval]").value = op?.interval / 1000 || 10;
    panel.querySelector("[data-cast-shuffle]").checked = !!op?.shuffled;
    panel.querySelector("[data-cast-loop]").checked = !!op?.loop;
  });
  window.filesCast = controller;
  function remoteState() {
    const op = controller.operation;
    if (op?.loaded && player.isConnected === false && !op.disconnected) { controller.disconnect(); return; }
    if (!op?.data || op.data.mode !== "video" || !owns(op)) return;
    panel.querySelector("[data-cast-toggle]").textContent = player.isPaused ? "Play" : "Pause";
    const seek = panel.querySelector("[data-cast-seek]");
    seek.disabled = !player.canSeek || !player.duration;
    seek.value = player.duration ? 100 * player.currentTime / player.duration : 0;
    panel.querySelector("[data-cast-time]").textContent = `${Math.floor(player.currentTime || 0)} / ${Math.floor(player.duration || 0)} seconds`;
    panel.querySelector("[data-cast-volume]").value = player.volumeLevel;
    if (op.loaded && !controller.flight && player.playerState === chrome.cast.media.PlayerState.IDLE && player.isMediaLoaded === false) {
      controller.stop("Video ended or was stopped on the receiver.");
    }
  }
  function availability() {
    const available = !!context && context.getCastState() !== cast.framework.CastState.NO_DEVICES_AVAILABLE;
    document.querySelectorAll("[data-cast]").forEach(button => {
      button.disabled = !available;
      button.title = available ? "Choose a Cast device" : "Requires HTTPS, supported Chrome, and a reachable Cast device";
    });
  }
  window.__onGCastApiAvailable = available => {
    if (!available) return;
    context = cast.framework.CastContext.getInstance();
    context.setOptions({ receiverApplicationId: chrome.cast.media.DEFAULT_MEDIA_RECEIVER_APP_ID,
      autoJoinPolicy: chrome.cast.AutoJoinPolicy.ORIGIN_SCOPED, resumeSavedSession: false });
    player = new cast.framework.RemotePlayer();
    remote = new cast.framework.RemotePlayerController(player);
    remote.addEventListener(cast.framework.RemotePlayerEventType.ANY_CHANGE, remoteState);
    context.addEventListener(cast.framework.CastContextEventType.CAST_STATE_CHANGED, availability);
    context.addEventListener(cast.framework.CastContextEventType.SESSION_STATE_CHANGED, event => {
      if ([cast.framework.SessionState.SESSION_ENDED, cast.framework.SessionState.SESSION_ENDING].includes(event.sessionState)) controller.disconnect();
    });
    availability();
  };
  function init() {
    availability();
    if (!document.querySelector("[data-cast]") || sdkRequested || !window.isSecureContext) return;
    sdkRequested = true;
    const script = document.createElement("script");
    script.src = "https://www.gstatic.com/cv/js/sender/v1/cast_sender.js?loadCastFramework=1";
    script.referrerPolicy = "no-referrer";
    script.onerror = () => { if (!controller.operation) controller.report("Cast SDK could not load. Check browser support and network access."); };
    document.head.append(script);
  }
  document.addEventListener("htmx:afterSwap", init);
  document.addEventListener("visibilitychange", () => controller.visibility(document.hidden));
  document.addEventListener("click", event => {
    const button = event.target.closest("[data-cast]");
    if (button) { event.preventDefault(); controller.start({ endpoint: button.dataset.cast, path: button.dataset.path, mode: button.dataset.mode }); }
    if (event.target.closest("[data-cast-stop]")) controller.stop();
    if (event.target.closest("[data-cast-toggle]")) controller.toggle();
    if (event.target.closest("[data-cast-next]")) controller.move(1);
    if (event.target.closest("[data-cast-previous]")) controller.move(-1);
    if (event.target.closest("[data-cast-retry]")) controller.retry();
  });
  document.addEventListener("change", event => {
    const op = controller.operation;
    if (event.target.matches("[data-cast-interval]")) controller.interval(event.target.value);
    if (event.target.matches("[data-cast-shuffle]")) controller.shuffle(event.target.checked);
    if (event.target.matches("[data-cast-loop]") && op) op.loop = event.target.checked;
    if (event.target.matches("[data-cast-seek]") && op && owns(op)) { player.currentTime = Number(event.target.value) * player.duration / 100; remote.seek(); }
    if (event.target.matches("[data-cast-volume]") && op && owns(op)) { player.volumeLevel = Number(event.target.value); remote.setVolumeLevel(); }
  });
  init();
})();
