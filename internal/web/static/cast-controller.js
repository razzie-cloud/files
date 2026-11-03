"use strict";
// Dependency-injected state machine, shared by the browser and fake-SDK tests.
// Capability URLs stay only in these document-local objects.
class FilesCastController {
  constructor(adapter, api, clock, changed) {
    this.adapter = adapter;
    this.api = api;
    this.clock = clock;
    this.changed = changed;
    this.operation = null;
    this.generation = 0;
    this.flight = null;
    this.retired = new Set();
    this.hidden = false;
  }
  current(op) { return this.operation === op && op.generation === this.generation; }
  report(message) { this.message = message; this.changed(this); }
  clear(op) { if (op?.timer != null) this.clock.clearTimeout(op.timer); if (op) op.timer = null; }
  cleanup(op) {
    if (!op?.data?.cleanup || op.cleaned) return;
    op.cleaned = true;
    // Revocation never waits for a receiver that may be offline.
    this.api.remove(op.data.cleanup).catch(() => {
      if (!this.operation) this.report("Stopped. Temporary share cleanup failed; revoke it in Sharing or let it expire.");
    });
  }
  stop(message = "Casting stopped.") {
    const op = this.operation;
    this.clear(op);
    this.operation = null;
    this.generation++;
    if (op) {
      op.playing = false;
      op.desired = null;
      this.cleanup(op);
      if (op.attempted.size) this.retired.add(op);
    }
    // Retain previous media ownership while a replacement is being prepared.
    // An aborted replacement must still stop that media. Wait for any LOAD,
    // and never send old Stop requests while a newer operation is active.
    const settled = this.flight || Promise.resolve();
    settled.catch(() => {}).then(async () => {
      while (!this.operation && this.retired.size) {
        const previous = this.retired.values().next().value;
        this.retired.delete(previous);
        try { await this.adapter.stop(previous); } catch (_) {}
      }
    });
    this.report(message);
  }
  async start(source) {
    let picker;
    try { picker = this.adapter.pick(); } catch (_) { this.report("Cast is unavailable. Use HTTPS in supported Chrome on the receiver's network."); return; }
    this.stop("");
    const op = { generation: this.generation, source, data: null, desired: null, displayed: null, timer: null,
      playing: !this.hidden, interval: 10000, loop: false, order: [], shuffled: false, attempted: new Set() };
    this.operation = op;
    this.report("Choose a Cast device…");
    try {
      op.session = await picker;
      if (!this.current(op)) return;
      this.report("Preparing playback…");
      const data = await this.api.create(source);
      op.data = data;
      if (!this.current(op) || op.disconnected) { this.cleanup(op); return; }
      op.order = (data.mode === "video" ? [data.media] : (data.entries ?? [])).map((_, i) => i);
      if (!op.order.length) throw { safeMessage: "This folder has no JPEG or PNG pictures." };
      op.desired = 0;
      this.arm(op, Infinity);
      this.drain();
    } catch (error) {
      if (!this.current(op)) return;
      this.stop(error?.cancelled ? "Device selection canceled." : (error?.safeMessage || "Unable to start casting. Check the source and device, then retry."));
    }
  }
  expired(op) { return this.clock.now() >= Date.parse(op.data.expires); }
  arm(op, delay) {
    this.clear(op);
    if (!this.current(op) || !op.data) return;
    const remaining = Date.parse(op.data.expires) - this.clock.now();
    if (remaining <= 0) { this.stop("Playback access expired. Start casting again to continue."); return; }
    op.timer = this.clock.setTimeout(() => {
      op.timer = null;
      if (!this.current(op)) return;
      if (this.expired(op)) { this.stop("Playback access expired. Start casting again to continue."); return; }
      this.move(1, true);
    }, Math.min(delay, remaining));
  }
  entry(op, position) { return op.data.mode === "video" ? op.data.media : op.data.entries[op.order[position]]; }
  async drain() {
    const op = this.operation;
    if (!op?.data || op.disconnected || op.desired == null || this.flight) return;
    if (this.expired(op)) { this.stop("Playback access expired. Start casting again to continue."); return; }
    const connection = op.connection;
    const position = op.desired;
    const entry = this.entry(op, position);
    this.arm(op, Infinity);
    op.attempted.add(entry.url);
    const pending = Promise.resolve().then(() => this.adapter.load(op, entry));
    this.flight = pending;
    let failed = false;
    try {
      await pending;
      if (!this.current(op) || connection !== op.connection) return;
      // The new LOAD has replaced previous media; stale operations no longer
      // need to be retained for a possible aborted replacement.
      this.retired.clear();
      op.displayed = position;
      op.failed = null;
      op.loaded = true;
      if (op.desired === position) {
        op.desired = null;
        this.report(op.data.mode === "video" ? `Casting ${entry.name}.` : `${entry.name} · ${position + 1} of ${op.order.length}${op.playing ? "" : " · Paused"}`);
        this.arm(op, op.data.mode === "slideshow" && op.playing ? op.interval : Infinity);
      }
    } catch (error) {
      if (!this.current(op) || connection !== op.connection) return;
      failed = true;
      if (!op.loaded || error?.authorization) {
        this.stop(error?.authorization ? "Playback access expired, was revoked, or the source disappeared. Start again to continue." : `Unable to cast ${entry.name}. Check device format support and try again.`);
      } else {
        // Leave manual retry/skip available without cycling through failures.
        op.playing = false;
        op.failed = position;
        if (op.desired === position) op.desired = null;
        this.report(`Unable to cast ${entry.name}. Retry or choose another picture.`);
        this.arm(op, Infinity);
      }
    } finally {
      if (this.flight === pending) this.flight = null;
      if (this.current(op) && op.pendingShuffle != null) {
        const enabled = op.pendingShuffle;
        op.pendingShuffle = null;
        this.shuffle(enabled);
      }
      // Dispatch only the latest requested selection, including a replacement
      // operation that had to wait for this operation's LOAD to settle.
      if (this.operation?.desired != null && (!failed || this.operation !== op || op.desired !== position)) this.drain();
    }
  }
  move(delta, automatic = false) {
    const op = this.operation;
    if (!op?.data || op.data.mode !== "slideshow") return;
    if (automatic && (!op.playing || this.hidden)) { this.arm(op, Infinity); return; }
    const from = op.desired ?? op.failed ?? op.displayed ?? 0;
    let next = from + delta;
    if (op.loop) next = (next + op.order.length) % op.order.length;
    else if (next < 0 || next >= op.order.length) {
      if (automatic) {
        op.playing = false;
        this.report("Slideshow finished. Choose Previous or enable Loop to continue.");
        this.arm(op, Infinity);
      }
      return;
    }
    op.desired = next;
    this.arm(op, Infinity);
    this.drain();
  }
  retry() { const op = this.operation; if (op?.data?.mode === "slideshow") { op.desired = op.desired ?? op.failed ?? op.displayed ?? 0; this.drain(); } }
  toggle() {
    const op = this.operation;
    if (!op?.data) return;
    if (op.disconnected) {
      if (!this.adapter.reconnect(op)) { this.report("Reconnect to the same Cast session or start a new cast."); return; }
      op.disconnected = false;
      if (op.data.mode === "slideshow") { op.desired = op.displayed ?? 0; this.drain(); }
    }
    if (op.data.mode === "video") { this.adapter.toggle(op); return; }
    op.playing = !op.playing && !this.hidden;
    this.report(op.playing ? "Slideshow playing." : "Slideshow paused. Keep this page active for timed advancement.");
    this.arm(op, op.playing && op.desired == null ? op.interval : Infinity);
  }
  interval(seconds) {
    const op = this.operation;
    if (!op?.data || op.data.mode !== "slideshow") return;
    op.interval = Math.max(2, Math.min(300, Number(seconds) || 10)) * 1000;
    this.arm(op, op.playing && op.desired == null ? op.interval : Infinity);
  }
  shuffle(enabled) {
    const op = this.operation;
    if (!op?.data || op.data.mode !== "slideshow") return;
    if (this.flight) { op.pendingShuffle = enabled; return; }
    const selected = op.order[op.desired ?? op.failed ?? op.displayed ?? 0];
    const failed = op.failed == null ? null : op.order[op.failed];
    const displayed = op.displayed == null ? null : op.order[op.displayed];
    op.shuffled = enabled;
    op.order = op.data.entries.map((_, i) => i);
    if (enabled) for (let i = op.order.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [op.order[i], op.order[j]] = [op.order[j], op.order[i]];
    }
    if (op.desired != null) op.desired = op.order.indexOf(selected);
    if (displayed != null) op.displayed = op.order.indexOf(displayed);
    if (failed != null) op.failed = op.order.indexOf(failed);
    this.changed(this);
  }
  visibility(hidden) {
    this.hidden = hidden;
    const op = this.operation;
    if (hidden && op && (!op.data || op.data.mode === "slideshow")) {
      op.playing = false;
      if (!op.data) return;
      this.arm(op, Infinity);
      this.report("Slideshow paused while the page is hidden. Return and select Resume; keep this page active.");
    }
  }
  disconnect() {
    const op = this.operation;
    if (!op) return;
    if (!op.data) { this.stop("Cast disconnected. Start casting again to continue."); return; }
    this.clear(op);
    op.connection = (op.connection || 0) + 1;
    op.desired = null;
    op.playing = false;
    op.disconnected = true;
    this.report("Cast disconnected. Reconnect, then explicitly resume or start casting again.");
    this.arm(op, Infinity);
  }
}
if (typeof module !== "undefined") module.exports = FilesCastController;
else window.FilesCastController = FilesCastController;
