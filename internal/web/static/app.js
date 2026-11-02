"use strict";

// Zahlenformate (deutsch)
const fmtDec = new Intl.NumberFormat("de-DE", { minimumFractionDigits: 1, maximumFractionDigits: 1 });
const fmtInt = new Intl.NumberFormat("de-DE", { maximumFractionDigits: 0 });
const formatters = {
  temp: (v) => fmtDec.format(v),
  dec: (v) => fmtDec.format(v),
  int: (v) => fmtInt.format(v),
  hours: (v) => fmtInt.format(v / 3600),
};

// Farbskalen für Temperaturen: kalt (blau) bis warm (orange/rot).
const tempScales = {
  outdoor: [-15, 35],
  water: [15, 60],
  room: [15, 26],
};

let state = null;
let hcBuilt = false;

function get(values, path) {
  const [group, field] = path.split(".");
  const obj = values[group];
  if (obj === undefined) return undefined;
  if (field === undefined) return obj;
  return obj[field];
}

function has(values, path) {
  const v = get(values, path);
  return v !== undefined && v !== null;
}

function tempColor(value, kind) {
  const [min, max] = tempScales[kind] || tempScales.water;
  const t = Math.min(1, Math.max(0, (value - min) / (max - min)));
  // Farbton von 215 (blau) über neutral nach 15 (orange-rot)
  const hue = 215 - t * 200;
  const sat = 20 + Math.abs(t - 0.5) * 140;
  return `hsl(${hue} ${sat}% 52%)`;
}

function buildHeatingCircuits(circuits) {
  const template = document.getElementById("hc-template");
  const anchor = template;
  for (const hc of circuits) {
    const node = template.content.firstElementChild.cloneNode(true);
    node.dataset.show = hc.key;
    node.querySelector(".hc-title").textContent = hc.label;
    node.querySelector("[data-mode]").dataset.mode = hc.key;
    for (const el of node.querySelectorAll("[data-field], [data-show], [data-ring]")) {
      for (const attr of ["field", "show", "ring"]) {
        if (el.dataset[attr]) el.dataset[attr] = el.dataset[attr].replaceAll("HC.", hc.key + ".");
      }
    }
    if (!hc.hasMixer) node.querySelector("[data-ring]").remove();
    anchor.before(node);
  }
  hcBuilt = true;
}

function setText(el, text) {
  if (el.textContent === text) return;
  const first = el.textContent === "–";
  el.textContent = text;
  if (!first) {
    el.classList.remove("changed");
    void el.offsetWidth; // Animation neu starten
    el.classList.add("changed");
  }
}

function render(s) {
  state = s;
  const v = s.values || {};

  if (!hcBuilt && s.heatingCircuits) buildHeatingCircuits(s.heatingCircuits);

  for (const el of document.querySelectorAll("[data-show]")) {
    const paths = el.dataset.show.split(" ");
    const visible = el.dataset.needsBoth ? paths.every((p) => has(v, p)) : paths.some((p) => has(v, p));
    el.classList.toggle("hidden", !visible);
  }

  for (const el of document.querySelectorAll("[data-field]")) {
    const value = get(v, el.dataset.field);
    if (value === undefined || value === null) continue;
    setText(el, (formatters[el.dataset.fmt] || String)(value));
    if (el.dataset.temp) el.style.color = tempColor(value, el.dataset.temp);
  }

  for (const el of document.querySelectorAll("[data-gauge]")) {
    const value = get(v, el.dataset.gauge);
    if (value === undefined) continue;
    const min = Number(el.dataset.min), max = Number(el.dataset.max);
    const pct = Math.min(100, Math.max(0, ((value - min) / (max - min)) * 100));
    const arc = el.querySelector(".value");
    arc.style.strokeDasharray = `${pct} 100`;
    arc.style.stroke = tempColor(value, "water");
  }

  for (const el of document.querySelectorAll("[data-ring]")) {
    const value = get(v, el.dataset.ring);
    if (value === undefined) continue;
    const pct = Math.min(100, Math.max(0, value));
    el.querySelector(".value").style.strokeDasharray = `${pct} 100`;
  }

  for (const el of document.querySelectorAll("[data-bar]")) {
    const max = Number(el.dataset.max);
    const actual = get(v, el.dataset.bar);
    const target = get(v, el.dataset.target);
    el.classList.toggle("hidden", actual === undefined);
    if (actual !== undefined) el.querySelector(".bar-fill").style.width = `${Math.min(100, (actual / max) * 100)}%`;
    const marker = el.querySelector(".bar-target");
    marker.classList.toggle("hidden", target === undefined);
    if (target !== undefined) marker.style.left = `calc(${Math.min(100, (target / max) * 100)}% - 1.5px)`;
  }

  for (const el of document.querySelectorAll("[data-mode]")) {
    const mode = get(v, el.dataset.mode + ".summerWinterMode");
    el.classList.toggle("hidden", !mode);
    if (mode) {
      el.textContent = mode;
      el.className = "badge " + mode.toLowerCase().replace("ü", "u");
    }
  }

  const supply = get(v, "temperatures.supply");
  const ret = get(v, "temperatures.return");
  if (supply !== undefined && ret !== undefined) setText(document.getElementById("spread"), fmtDec.format(supply - ret));

  const active = (get(v, "power.compressorPower") || 0) > 0;
  document.getElementById("fan").classList.toggle("spin", active);
  document.getElementById("pipe").classList.toggle("active", active);

  setChip("chip-gateway", s.lastPoll ? s.gatewayOk : null, "Gateway");
  setChip("chip-mqtt", s.lastPoll ? s.mqttOk : null, "MQTT", s.mqttError);
  document.getElementById("version").textContent = s.version ? "buderus2mqtt " + s.version : "";
  updateAge();
}

function setChip(id, ok, label, title) {
  const el = document.getElementById(id);
  el.classList.toggle("ok", ok === true);
  el.classList.toggle("err", ok === false);
  el.textContent = label + (ok === null ? "" : ok ? " verbunden" : " Fehler");
  el.title = title || "";
}

function formatAge(seconds) {
  if (seconds < 60) return `vor ${Math.max(0, Math.round(seconds))} s`;
  if (seconds < 3600) return `vor ${Math.round(seconds / 60)} min`;
  return `vor ${fmtDec.format(seconds / 3600)} h`;
}

function updateAge() {
  const el = document.getElementById("last-poll");
  const banner = document.getElementById("stale");
  if (!state || !state.lastPoll) {
    el.textContent = "Warte auf Daten …";
    banner.classList.add("hidden");
    return;
  }
  const age = (Date.now() - new Date(state.lastPoll).getTime()) / 1000;
  el.textContent = "Letzte Abfrage " + formatAge(age);
  el.title = new Date(state.lastPoll).toLocaleString("de-DE");
  banner.classList.toggle("hidden", age <= 2 * state.pollIntervalSeconds + 30);
}

function connect() {
  const live = document.getElementById("live");
  const source = new EventSource("api/events");
  source.addEventListener("state", (e) => render(JSON.parse(e.data)));
  source.onopen = () => live.classList.add("on");
  // EventSource verbindet sich selbst neu; hier nur den Status anzeigen.
  source.onerror = () => live.classList.remove("on");
}

setInterval(updateAge, 1000);
connect();
