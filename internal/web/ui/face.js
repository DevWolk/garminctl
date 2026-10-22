'use strict';
// Easy+ watch face emulator: draws the face from the app's settings values.
// Geometry follows the store screenshots of the Round layout on a 390x390 AMOLED.
window.EasyFace = (() => {
  const APP_ID = '7c993e2b-70b3-459a-bbe0-db48a89af5a8';
  const W = 390, C = 195;

  // Metric ids as in the store settings definition.
  const METRIC = {
    24: { icon: null, val: () => '' },
    27: { icon: 'activity', val: () => '38' }, 21: { icon: 'activity', val: () => '142' },
    20: { icon: 'bell', val: () => '2' }, 43: { icon: 'mountain', val: () => '214m' },
    14: { icon: 'battery', val: () => '78%' }, 42: { icon: 'battery', val: () => '9d' },
    38: { icon: 'bodybattery', val: () => '64' }, 1: { icon: 'flame', val: () => '1840' }, 17: { icon: 'flame', val: () => '412' },
    54: { icon: 'star', val: (v) => '12', iconText: (v) => v.custom_compl_text }, 35: { icon: 'text', val: (v) => v.custom_subtext || '', iconText: (v) => v.custom_text },
    25: { icon: 'clock', val: (v) => offsetTime(v.time1_utc) }, 34: { icon: 'clock', val: (v) => offsetTime(v.time2_utc) },
    15: { icon: 'date', val: (v) => dateText(v).value, iconText: (v) => dateText(v).icon },
    2: { icon: 'route', val: () => '6.2' }, 3: { icon: 'stairs', val: () => '12' }, 4: { icon: 'heart', val: () => '62' },
    19: { icon: 'heart', val: () => '48' }, 32: { icon: 'heart', val: () => '121' }, 31: { icon: 'heart', val: () => '54' },
    18: { icon: 'drop', val: () => '1.2' }, 40: { icon: 'moon', val: () => '72%' }, 5: { icon: 'message', val: () => '3', attention: true },
    29: { icon: 'o2', val: () => '97%' }, 6: { icon: 'gauge', val: () => '1013↑' }, 41: { icon: 'gauge', val: () => '1009' },
    36: { icon: 'activity', val: () => '14' }, 7: { icon: 'thermo', val: () => '24°' }, 16: { icon: 'steps', val: () => '7275' },
    33: { icon: 'steps', val: () => '7250' }, 39: { icon: 'stress', val: () => '23' }, 9: { icon: 'sunrise', val: () => '7:12' },
    10: { icon: 'sunset', val: () => '18:03' }, 11: { icon: 'sunset', val: () => '18:03' }, 37: { icon: 'clock', val: () => '12h' },
    49: { icon: 'activity', val: () => '44' }, 48: { icon: 'activity', val: () => '47' }, 22: { icon: 'thermo', val: () => '11°' },
    44: { icon: 'drop', val: () => '8°' }, 28: { icon: 'drop', val: () => '71%' }, 45: { icon: 'rain', val: () => '20%' },
    8: { icon: 'clock', val: () => '9:40' }, 12: { icon: 'cloud', val: () => '14°' }, 53: { icon: 'thermo', val: () => '16/9' },
    13: { icon: 'wind', val: () => '12 NW' }, 51: { icon: 'sun', val: () => '1.6' }, 47: { icon: 'route', val: () => '12.4' },
    26: { icon: 'calendar', val: () => isoWeek() }, 50: { icon: 'activity', val: () => '0' },
    30: { icon: 'status', val: () => '' },
  };
  const BAR_FILL = { 0: null, 105: .6, 102: .78, 107: .64, 106: .5, 110: .45, 101: .4, 100: .6, 109: 1, 104: .72, 103: .23, 111: .3 };
  const ICON = {
    heart: 'M20.8 4.6a5.5 5.5 0 0 0-7.8 0L12 5.7l-1-1.1a5.5 5.5 0 0 0-7.8 7.8l1 1L12 21l7.8-7.6 1-1a5.5 5.5 0 0 0 0-7.8z',
    battery: 'M2 7h16a1 1 0 0 1 1 1v8a1 1 0 0 1-1 1H2a1 1 0 0 1-1-1V8a1 1 0 0 1 1-1zM22 10v4 M4 10h8v4H4z',
    bodybattery: 'M2 7h16a1 1 0 0 1 1 1v8a1 1 0 0 1-1 1H2a1 1 0 0 1-1-1V8a1 1 0 0 1 1-1zM22 10v4 M11 8l-3 5h4l-3 5',
    bluetooth: 'M6.5 6.5l11 11L12 23V1l5.5 5.5-11 11',
    message: 'M21 11.5a8.4 8.4 0 0 1-9 8.4 8.4 0 0 1-3.6-.8L3 21l1.9-5.4A8.4 8.4 0 1 1 21 11.5z',
    sun: 'M12 7a5 5 0 1 0 0 10 5 5 0 0 0 0-10zM12 1v3M12 20v3M4.2 4.2l2.1 2.1M17.7 17.7l2.1 2.1M1 12h3M20 12h3M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1',
    sunrise: 'M17 18a5 5 0 0 0-10 0M12 2v7M4.2 10.2l1.4 1.4M1 18h2M21 18h2M18.4 11.6l1.4-1.4M23 22H1M8 6l4-4 4 4',
    sunset: 'M17 18a5 5 0 0 0-10 0M12 9V2M4.2 10.2l1.4 1.4M1 18h2M21 18h2M18.4 11.6l1.4-1.4M23 22H1M16 5l-4 4-4-4',
    cloud: 'M18 10h-1.3A8 8 0 1 0 9 20h9a5 5 0 0 0 0-10z',
    rain: 'M18 8h-1.3A8 8 0 1 0 9 18h9a5 5 0 0 0 0-10zM8 20v3M12 20v3M16 20v3',
    drop: 'M12 2.7l5.7 5.6a8 8 0 1 1-11.3 0z',
    steps: 'M8 3c2 0 3 2.5 3 5.5S10 14 8 14s-3-2.5-3-5.5S6 3 8 3zM7 15.5h2.5v3H7zM16 8c2 0 3 2.5 3 5.5S18 19 16 19s-3-2.5-3-5.5S14 8 16 8zM15 20.5h2.5v2.5H15z',
    stairs: 'M3 21h4v-4h4v-4h4V9h4V5h2',
    flame: 'M12 22c4.4 0 7-2.9 7-7 0-3.2-2-5.6-3.4-7.3-.4 1.6-1.4 2.6-2.4 3.1C13.6 7.5 12.4 4.5 9 2c.6 3.6-1.6 5.5-3 8.2A8.3 8.3 0 0 0 5 15c0 4.1 2.6 7 7 7z',
    moon: 'M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z',
    clock: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM12 7v5l3 2',
    bell: 'M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9M13.7 21a2 2 0 0 1-3.4 0',
    wind: 'M9.6 4.6A2 2 0 1 1 11 8H2M12.6 19.4A2 2 0 1 0 14 16H2M17.7 7.7A2.5 2.5 0 1 1 19.5 12H2',
    thermo: 'M14 14.8V3.5a2.5 2.5 0 0 0-5 0v11.3a4 4 0 1 0 5 0z',
    mountain: 'M3 20l6-10 4 6 2-3 6 7z',
    gauge: 'M4 18a9 9 0 1 1 16 0M12 17l4-6',
    stress: 'M2 13c2 0 2-5 4-5s2 7 4 7 2-9 4-9 2 7 4 7 2-3 4-3',
    activity: 'M22 12h-4l-3 9L9 3l-3 9H2',
    calendar: 'M3 6a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2zM3 10h18M8 2v4M16 2v4',
    route: 'M4 19a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM20 9a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM6 17c4 0 2-8 6-8s2 6 6 0',
    star: 'M12 2l2.4 7.4H22l-6.2 4.5 2.4 7.4L12 16.8l-6.2 4.5 2.4-7.4L2 9.4h7.6z',
  };
  const S2THEMES = { 0: ['FF7A00', '000000'], 1: ['00FF00', '000000'], 2: ['00AAFF', '000000'], 3: ['FF0000', '000000'], 4: ['FFFF00', '000000'], 5: ['FFFFFF', '000000'], 6: ['000000', 'FFFFFF'], 7: ['FF0000', 'FFFFFF'] };

  function hex(n) { return '#' + (Number(n) >>> 0 & 0xFFFFFF).toString(16).padStart(6, '0'); }
  function pad(n) { return String(n).padStart(2, '0'); }
  function offsetTime(off) {
    const d = new Date();
    const m = d.getUTCHours() * 60 + d.getUTCMinutes() + Math.round(Number(off || 0) * 60);
    const mm = ((m % 1440) + 1440) % 1440;
    return `${Math.floor(mm / 60)}:${pad(mm % 60)}`;
  }
  function isoWeek() {
    const d = new Date(); const t = new Date(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()));
    const day = t.getUTCDay() || 7; t.setUTCDate(t.getUTCDate() + 4 - day);
    const y0 = new Date(Date.UTC(t.getUTCFullYear(), 0, 1));
    return String(Math.ceil(((t - y0) / 864e5 + 1) / 7));
  }
  function dateText(v) {
    const d = new Date();
    const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
    const days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
    const dd = d.getDate(), mo = months[d.getMonth()], wd = days[d.getDay()];
    let icon = wd, value;
    switch (Number(v.dt_format)) {
      case 1: value = `${mo} ${dd}`; break;
      case 2: value = `${dd} ${mo}`; break;
      case 3: value = `${pad(dd)}/${pad(d.getMonth() + 1)}`; break;
      case 4: value = `${pad(d.getMonth() + 1)}/${pad(dd)}`; break;
      case 5: value = wd; icon = String(dd); break;
      default: value = String(dd);
    }
    if (v.uppr_dt) { value = value.toUpperCase(); icon = icon.toUpperCase(); }
    return { icon, value };
  }

  // Resolves a palette value to a CSS colour. base: colour for -2 ("base" choice).
  function color(v, val, base) {
    const n = Number(val);
    if (Number.isNaN(n)) return base || '#fff';
    if (n >= 0) return hex(n);
    if (n === -11) return '#' + (v.cust_color1 || 'FF0000');
    if (n === -12) return '#' + (v.cust_color2 || '00FF00');
    if (n === -13) return '#' + (v.cust_color3 || '0000FF');
    if (n === -2) return base || '#fff';
    return 'transparent';
  }

  function fontStyle(code) {
    switch (Number(code)) {
      case 1: return { weight: 700, outline: true };
      case 2: return { weight: 300 };
      case 3: return { weight: 400 };
      case 4: return { weight: 700 };
      case 5: return { weight: 900 };
      case 6: return { weight: 700, italic: true };
      default: return { weight: 700 };
    }
  }

  function luminance(cssHex) {
    const n = parseInt(cssHex.slice(1), 16);
    return (0.299 * (n >> 16 & 255) + 0.587 * (n >> 8 & 255) + 0.114 * (n & 255)) / 255;
  }

  // Builds the palette for a screen (1 or 2) honouring the screen-2 theme.
  function palette(v, screen) {
    const p = {
      bg: color(v, v.bg_color, '#000'),
      hour: color(v, v.hour_color), hour2: Number(v.hour_color2) === -2 ? null : color(v, v.hour_color2),
      min: color(v, v.min_color), min2: Number(v.min_color2) === -2 ? null : color(v, v.min_color2),
      metric: color(v, v.base_metric), icon: color(v, v.base_icon),
      lines: color(v, v.lines_color, '#555'), linesBg: Number(v.lines_bg) === -1 ? 'transparent' : color(v, v.lines_bg, 'transparent'),
      bar1: color(v, v.bar1a_color), bar1b: color(v, v.bar1b_color), bar2: color(v, v.bar2a_color), bar2b: color(v, v.bar2b_color),
      spot: color(v, v.spot_color), attention: Number(v.attention_color) === -1 ? null : color(v, v.attention_color),
      perMetric: (i) => color(v, v['metric' + i], color(v, v.base_metric)),
      perIcon: (i) => color(v, v['icon' + i], color(v, v.base_icon)),
    };
    if (screen === 2) {
      const t = S2THEMES[Number(v.s2theme)];
      if (t) {
        const fg = '#' + t[0], bg = '#' + t[1];
        const dim = luminance(bg) > .5 ? '#999999' : '#555555';
        Object.assign(p, { bg, hour: fg, hour2: null, min: fg, min2: null, metric: fg, icon: fg, lines: fg, linesBg: 'transparent', bar1: fg, bar1b: dim, bar2: fg, bar2b: dim, spot: fg });
        p.perMetric = () => fg; p.perIcon = () => fg;
      }
    }
    return p;
  }

  function polar(angleDeg, r) { const a = angleDeg * Math.PI / 180; return [C + r * Math.cos(a), C - r * Math.sin(a)]; }
  function arcPath(r, a0, a1) {
    const [x0, y0] = polar(a0, r), [x1, y1] = polar(a1, r);
    const large = Math.abs(a1 - a0) > 180 ? 1 : 0;
    const sweep = a1 < a0 ? 1 : 0;
    return `M${x0.toFixed(1)} ${y0.toFixed(1)}A${r} ${r} 0 ${large} ${sweep} ${x1.toFixed(1)} ${y1.toFixed(1)}`;
  }

  // Field slots on the round layout, measured from photos of the watch: every
  // value stays upright; the icon sits on the side nearer the time (below the
  // value in the upper half and beside it on the left/right, above the value
  // in the lower half). Offsets from the centre in pixels, y down.
  const SLOTS = {
    1: { tx: 0, ty: -150, ix: 0, iy: -118 }, 2: { tx: 90, ty: -118, ix: 82, iy: -84 }, 3: { tx: 138, ty: -34, ix: 138, iy: 2 },
    4: { tx: 97, ty: 98, ix: 92, iy: 62 }, 5: { tx: 0, ty: 152, ix: 0, iy: 118 }, 6: { tx: -97, ty: 98, ix: -92, iy: 62 },
    7: { tx: -138, ty: -34, ix: -138, iy: 2 }, 8: { tx: -90, ty: -118, ix: -82, iy: -84 },
  };

  function esc(s) { return String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])); }

  function iconSvg(name, x, y, size, col, text) {
    if (name === 'text' || name === 'date' || name === 'o2' || text !== undefined) {
      const t = text !== undefined && text !== null && text !== '' ? text : (name === 'o2' ? 'O₂' : '');
      return `<text x="${x}" y="${y}" font-size="${size * 0.78}" font-weight="600" fill="${col}" text-anchor="middle" dominant-baseline="central" font-family="Bahnschrift, 'DIN Alternate', 'Segoe UI', system-ui, sans-serif">${esc(t)}</text>`;
    }
    const d = ICON[name];
    if (!d) return `<circle cx="${x}" cy="${y}" r="${size * 0.12}" fill="${col}"/>`;
    const s = size / 24;
    return `<g transform="translate(${x - size / 2} ${y - size / 2}) scale(${s})"><path d="${d}" fill="none" stroke="${col}" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/></g>`;
  }

  let seq = 0;
  function render(v, opts) {
    // Every render gets its own id namespace: two faces on one page (Overview hero and the editor)
    // would otherwise share gradient ids, and url(#id) resolves to the first one in the document
    // even when that one sits inside a hidden tab and paints nothing.
    const ns = `ef${++seq}-`;
    const screen = opts.screen === 2 && v.secondscreen ? 2 : 1;
    const sfx = screen === 2 ? 'b' : 'a';
    const p = palette(v, screen);
    const aod = opts.aod ? Number(v.aod_type) : 4;
    const layout = Number(v.layout_type) || 1;
    const scale = Math.min(1.7, Math.max(0.6, Number(v.metrics_scale) || 1));
    const now = new Date();
    let h = now.getHours();
    const h24 = Number(v.hours24) === 2 || (Number(v.hours24) === 0 && opts.systemIs24h);
    const pm = h >= 12;
    if (!h24) { h = h % 12; if (h === 0) h = 12; }
    const hh = v.leading0 ? pad(h) : String(h);
    const mm = pad(now.getMinutes());
    const ss = pad(now.getSeconds());
    const parts = [];
    const defs = [];
    parts.push(`<circle cx="${C}" cy="${C}" r="${C}" fill="${p.bg}"/>`);
    if (aod === 0) return wrap(parts.join(''), defs, ns);

    // Spotlight (AMOLED glow) behind everything.
    const spot = Number(v.spot_type);
    const gradientFonts = !!(p.hour2 && p.hour2 !== p.hour) || !!(p.min2 && p.min2 !== p.min);
    if (spot >= 0 && !opts.aod) {
      if ((spot === 0 || spot === 1) && !gradientFonts) {
        defs.push(`<radialGradient id="${ns}spot"><stop offset="0" stop-color="${p.spot}" stop-opacity="${spot === 1 ? .55 : .45}"/><stop offset="${spot === 1 ? .75 : .5}" stop-color="${p.spot}" stop-opacity="0"/></radialGradient>`);
        parts.push(`<circle cx="${C}" cy="${C}" r="${C}" fill="url(#${ns}spot)"/>`);
      } else if (spot === 2 || spot === 3) {
        defs.push(`<radialGradient id="${ns}spot"><stop offset="${spot === 3 ? .55 : .8}" stop-color="${p.spot}" stop-opacity="0"/><stop offset="1" stop-color="${p.spot}" stop-opacity=".6"/></radialGradient>`);
        parts.push(`<circle cx="${C}" cy="${C}" r="${C}" fill="url(#${ns}spot)"/>`);
      } else if (spot === 4) {
        defs.push(`<radialGradient id="${ns}spot"><stop offset=".45" stop-color="${p.spot}" stop-opacity="0"/><stop offset=".72" stop-color="${p.spot}" stop-opacity=".35"/><stop offset="1" stop-color="${p.spot}" stop-opacity="0"/></radialGradient>`);
        parts.push(`<circle cx="${C}" cy="${C}" r="${C}" fill="url(#${ns}spot)"/>`);
      }
    }

    // Separator lines between the eight fields (round layouts only).
    if (layout !== 2 && aod >= 3) {
      const w = Math.min(5, Math.max(1, Number(v.lines_width) || 2));
      for (let k = 0; k < 8; k++) {
        const a = 22.5 + 45 * k;
        const [x0, y0] = polar(a, 96), [x1, y1] = polar(a, 196);
        if (p.linesBg !== 'transparent') parts.push(`<line x1="${x0}" y1="${y0}" x2="${x1}" y2="${y1}" stroke="${p.linesBg}" stroke-width="${w + 4}" stroke-linecap="butt"/>`);
        parts.push(`<line x1="${x0}" y1="${y0}" x2="${x1}" y2="${y1}" stroke="${p.lines}" stroke-width="${w}" stroke-linecap="butt"/>`);
      }
    }

    // Progress bars left and right of the time.
    if (aod >= 4) {
      for (const side of ['1', '2']) {
        const type = Number(v['bar' + side + sfx]);
        const fill = BAR_FILL[type];
        if (fill === null || fill === undefined) continue;
        const main = side === '1' ? p.bar1 : p.bar2, second = side === '1' ? p.bar1b : p.bar2b;
        const centre = side === '1' ? 180 : 0, span = 33, segs = 5, r = 97;
        for (let i = 0; i < segs; i++) {
          const a0 = centre - span + (2 * span) * i / segs + 1.6, a1 = centre - span + (2 * span) * (i + 1) / segs - 1.6;
          const filled = side === '1' ? i < Math.round(fill * segs) : i >= segs - Math.round(fill * segs);
          const lit = side === '1' ? filled : filled;
          parts.push(`<path d="${arcPath(r, side === '1' ? a0 : a0, side === '1' ? a1 : a1)}" stroke="${lit ? main : second}" stroke-width="14" fill="none" data-key="bar${side}${sfx}" class="face-hit"/>`);
        }
      }
    }

    // Time.
    const hf = fontStyle(v.t_font), mf = Number(v.m_font) === 0 ? hf : fontStyle(v.m_font);
    const timeFont = `Bahnschrift, 'DIN Alternate', 'Arial Narrow', 'Segoe UI', system-ui, sans-serif`;
    const dimAod = opts.aod && v.reduced_aod ? ' opacity=".55"' : '';
    function timeText(txt, y, f, c1, c2, id) {
      let fill = c1;
      if (c2 && c2 !== c1) { defs.push(`<linearGradient id="${ns}${id}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${c1}"/><stop offset="1" stop-color="${c2}"/></linearGradient>`); fill = `url(#${ns}${id})`; }
      const stroke = f.outline ? ` fill="none" stroke="${fill}" stroke-width="3"` : ` fill="${fill}"`;
      return `<text x="${C}" y="${y}" font-size="98" font-weight="${f.weight}" font-stretch="condensed" ${f.italic ? 'font-style="italic"' : ''} font-family="${timeFont}" text-anchor="middle" dominant-baseline="central" letter-spacing="-2"${stroke}${dimAod} data-key="${id === 'gh' ? 't_font' : 'm_font'}" class="face-hit">${txt}</text>`;
    }
    parts.push(timeText(hh, 158, hf, p.hour, p.hour2, 'gh'));
    parts.push(timeText(mm, 232, mf, p.min, p.min2, 'gm'));

    // Fields.
    const visible = (i) => aod >= 3 || (aod === 2 && (i === 3 || i === 7));
    const base = 30 * scale;
    const metricFont = `'Segoe UI', Roboto, 'Helvetica Neue', system-ui, sans-serif`;
    for (let i = 1; i <= 8; i++) {
      if (!visible(i)) continue;
      const key = 'field' + i + sfx;
      const id = Number(v[key]);
      const m = METRIC[id];
      if (!m || id === 24) continue;
      const mc = p.perMetric(i), ic = p.perIcon(i);
      let text, iconName = m.icon, iconText;
      if (id === 30) {
        text = v.seconds ? ss : (v.am && !h24 ? (pm ? 'PM' : 'AM') : '');
        iconName = v.repl_stus ? 'message' : 'bluetooth';
      } else {
        text = m.val(v);
        if (m.iconText) iconText = m.iconText(v);
      }
      let iconCol = ic;
      if (m.attention && p.attention) iconCol = p.attention;
      const s = SLOTS[i];
      const fs = (v.metrics_even ? base : base * (String(text).length > 5 ? 0.82 : 1));
      let tx = C + s.tx, ty = C + s.ty, ix = C + s.ix, iy = C + s.iy;
      if (layout === 2) { // classic: a 3x3 grid
        const grid = { 1: [C, 48], 2: [C + 118, 92], 3: [C + 135, C], 4: [C + 118, 298], 5: [C, 342], 6: [C - 118, 298], 7: [C - 135, C], 8: [C - 118, 92] };
        [tx, ty] = grid[i]; ix = tx; iy = ty - fs * 0.95;
      }
      parts.push(`<g data-key="${key}" class="face-hit"><title>Field ${i}: ${esc(opts.labelFor ? opts.labelFor(key, id) : id)}</title>`);
      parts.push(iconSvg(iconName, ix, iy, 30 * scale, iconCol, iconText));
      parts.push(`<text x="${tx}" y="${ty}" font-size="${fs}" font-weight="700" fill="${mc}" text-anchor="middle" dominant-baseline="central" font-family="${metricFont}">${esc(text)}</text>`);
      parts.push('</g>');
    }
    return wrap(parts.join(''), defs, ns);
  }

  function wrap(body, defs, ns) {
    return `<svg viewBox="0 0 ${W} ${W}" width="100%" height="100%" role="img" aria-label="Watch face preview"><defs><clipPath id="${ns}faceclip"><circle cx="${C}" cy="${C}" r="${C}"/></clipPath>${defs.join('')}</defs><g clip-path="url(#${ns}faceclip)">${body}</g><circle cx="${C}" cy="${C}" r="${C - 1}" fill="none" stroke="rgba(255,255,255,.08)" stroke-width="2"/></svg>`;
  }

  return { APP_ID, render, supports: (appId) => String(appId).toLowerCase() === APP_ID };
})();
