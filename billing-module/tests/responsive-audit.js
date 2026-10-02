/* ==========================================================================
   EyvesCloud 客户区 —— 浏览器端设计体检
   用法：在客户区实例页面打开开发者工具，粘贴本文件全部内容回车。
        返回一份 JSON 报告，任何一项非空都说明有可改进之处。

   检查项：横向溢出 / 文本对比度（WCAG AA）/ 触控目标尺寸 /
          页签溢出提示 / 指标条行数 / 内容宽度约束
   ========================================================================== */
(function () {
  'use strict';

  function lum(c) {
    var p = c.match(/\d+/g).map(Number).map(function (v) {
      v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
    });
    return 0.2126 * p[0] + 0.7152 * p[1] + 0.0722 * p[2];
  }
  function ratio(a, b) {
    var l1 = lum(a), l2 = lum(b);
    var hi = Math.max(l1, l2), lo = Math.min(l1, l2);
    return (hi + 0.05) / (lo + 0.05);
  }
  function bgOf(el) {
    var e = el;
    while (e) {
      var c = getComputedStyle(e).backgroundColor;
      if (c && c !== 'rgba(0, 0, 0, 0)' && c !== 'transparent') return c;
      e = e.parentElement;
    }
    return 'rgb(255,255,255)';
  }

  var report = {
    viewport: window.innerWidth + 'x' + window.innerHeight,
    horizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 1,
    overflowingElements: [],
    contrastFailures: [],
    touchTargetsTooSmall: [],
    tabsOverflowHint: null,
    statStripRows: null,
    contentWidth: null
  };

  /* 1. 横向溢出：定位到具体元素，方便直接改 */
  document.querySelectorAll('.ev-panel *').forEach(function (el) {
    var r = el.getBoundingClientRect();
    if (!r.width) return;
    if (r.right > window.innerWidth + 1 || r.left < -1) {
      if (report.overflowingElements.length < 8) {
        report.overflowingElements.push({
          tag: el.tagName, cls: (el.className || '').toString().slice(0, 48),
          left: Math.round(r.left), right: Math.round(r.right)
        });
      }
    }
  });

  /* 2. 对比度：按 WCAG AA（正文 4.5:1，大字号 3:1） */
  document.querySelectorAll('.ev-panel *').forEach(function (el) {
    var hasText = Array.prototype.some.call(el.childNodes, function (n) {
      return n.nodeType === 3 && n.textContent.trim().length > 1;
    });
    if (!hasText) return;
    var r = el.getBoundingClientRect();
    if (!r.width || !r.height) return;
    var cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.opacity === '0') return;
    var cr = ratio(cs.color, bgOf(el));
    var size = parseFloat(cs.fontSize), bold = parseInt(cs.fontWeight, 10) >= 700;
    var need = (size >= 24 || (size >= 18.66 && bold)) ? 3 : 4.5;
    if (cr < need - 0.02) {
      report.contrastFailures.push({
        text: el.textContent.trim().slice(0, 20),
        cls: (el.className || '').toString().slice(0, 40),
        ratio: +cr.toFixed(2), need: need, fontSize: cs.fontSize
      });
    }
  });

  /* 3. 触控目标：可点区域小于 36px 在手机上不好按 */
  document.querySelectorAll('.ev-panel button, .ev-panel a, .ev-copyable, .ev-input, .ev-select').forEach(function (el) {
    var r = el.getBoundingClientRect();
    if (!r.width) return;
    if (r.height < 36 || r.width < 36) {
      report.touchTargetsTooSmall.push({
        label: (el.textContent || '').trim().slice(0, 16) || el.tagName,
        w: Math.round(r.width), h: Math.round(r.height)
      });
    }
  });

  /* 4. 页签溢出提示：滚动条隐藏时必须靠渐隐告诉用户「还有更多」 */
  var wrap = document.querySelector('[data-ev-tabswrap]');
  if (wrap) {
    var nav = wrap.querySelector('.ev-tabs');
    report.tabsOverflowHint = {
      scrollable: nav ? nav.scrollWidth - nav.clientWidth > 2 : false,
      hasFadeClass: wrap.classList.contains('is-overflow') || wrap.classList.contains('is-atend'),
      hasLeftFade: wrap.classList.contains('is-scrolled') || nav.scrollLeft <= 2
    };
  }

  /* 5. 指标条：不应出现只放一两格子的残缺行 */
  var stats = document.querySelectorAll('.ev-stat');
  if (stats.length) {
    var tops = {};
    stats.forEach(function (el) { tops[Math.round(el.getBoundingClientRect().top)] = (tops[Math.round(el.getBoundingClientRect().top)] || 0) + 1; });
    var counts = Object.keys(tops).map(function (k) { return tops[k]; });
    report.statStripRows = { rows: counts.length, perRow: counts };
  }

  /* 6. 内容宽度：宽屏不加最大宽度会松散 */
  var panel = document.querySelector('.ev-panel');
  if (panel) {
    report.contentWidth = {
      panel: Math.round(panel.getBoundingClientRect().width),
      maxWidth: getComputedStyle(panel).maxWidth
    };
  }

  var problems = (report.horizontalOverflow ? 1 : 0)
    + report.overflowingElements.length
    + report.contrastFailures.length
    + report.touchTargetsTooSmall.length
    + (report.tabsOverflowHint && report.tabsOverflowHint.scrollable && !report.tabsOverflowHint.hasFadeClass ? 1 : 0);

  console.log('%cEyvesCloud 设计体检', 'font-weight:700;font-size:13px');
  console.log(problems === 0 ? '%c✅ 未发现问题' : '%c⚠️ 发现 ' + problems + ' 处，见下表',
              problems === 0 ? 'color:#0f9d58' : 'color:#b45309');
  console.table(report.contrastFailures.length ? report.contrastFailures : [{ '对比度': '全部通过' }]);
  if (report.touchTargetsTooSmall.length) console.table(report.touchTargetsTooSmall);
  if (report.overflowingElements.length) console.table(report.overflowingElements);
  console.log(report);
  return report;
})();
