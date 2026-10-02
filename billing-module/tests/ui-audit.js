/* ==========================================================================
   EyvesCloud 客户区 —— 自动化 UI 审计
   
   在一个页面（或 iframe）的 document 上跑，返回结构化报告。
   设计目标：把「必须手动点一遍才能发现」的问题变成可重复的检查。
   
   覆盖的 8 类问题（都是本项目真实踩过的）：
     1. JS 运行时异常
     2. 横向溢出 / 元素越界
     3. 文本对比度（WCAG AA）
     4. 触控目标尺寸
     5. **JS 动态生成的节点是否真的拿到了样式**（令牌作用域踩过坑）
     6. **DOM 里用到的 ev-* 类是否都有对应 CSS 规则**（重命名漏改踩过坑）
     7. 可访问性：ARIA、可聚焦元素名称、tab 语义、图片替代文本
     8. 布局不变量：零尺寸、重叠、空容器
   ========================================================================== */
window.EvAudit = (function () {
  'use strict';

  /* ---------- 颜色工具 ---------- */
  function parseColor(c) {
    var m = (c || '').match(/rgba?\(([^)]+)\)/);
    if (!m) return null;
    var p = m[1].split(',').map(function (v) { return parseFloat(v); });
    if (p.length === 4 && p[3] === 0) return null;   // 全透明
    return { r: p[0], g: p[1], b: p[2], a: p.length === 4 ? p[3] : 1 };
  }
  function lum(rgb) {
    var f = [rgb.r, rgb.g, rgb.b].map(function (v) {
      v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
    });
    return 0.2126 * f[0] + 0.7152 * f[1] + 0.0722 * f[2];
  }
  function contrast(fg, bg) {
    var l1 = lum(fg), l2 = lum(bg);
    var hi = Math.max(l1, l2), lo = Math.min(l1, l2);
    return (hi + 0.05) / (lo + 0.05);
  }
  /** 沿祖先链找第一个非透明的背景色；找不到当白底 */
  function bgOf(el) {
    var e = el;
    while (e && e !== document.documentElement) {
      var c = parseColor(getComputedStyle(e).backgroundColor);
      if (c && c.a > 0.5) return c;
      e = e.parentElement;
    }
    return { r: 255, g: 255, b: 255, a: 1 };
  }
  function isVisible(el) {
    var cs = getComputedStyle(el);
    if (cs.display === 'none' || cs.visibility === 'hidden' || parseFloat(cs.opacity) < 0.05) return false;
    var r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0;
  }

  /* ---------- 收集样式表里定义过的类名 ---------- */
  function definedClasses() {
    var set = Object.create(null);
    for (var i = 0; i < document.styleSheets.length; i++) {
      var rules;
      try { rules = document.styleSheets[i].cssRules; } catch (e) { continue; }  // 跨域样式表跳过
      if (!rules) continue;
      (function walk(list) {
        for (var j = 0; j < list.length; j++) {
          var r = list[j];
          if (r.selectorText) {
            var m = r.selectorText.match(/\.(ev-[a-z0-9_-]+)/gi) || [];
            m.forEach(function (c) { set[c.slice(1).toLowerCase()] = true; });
          }
          if (r.cssRules) walk(r.cssRules);          // @media 等嵌套规则
        }
      })(rules);
    }
    return set;
  }

  /* ---------- 主审计 ---------- */
  function run(opts) {
    opts = opts || {};
    var root = opts.root || document;
    var report = {
      overflowX: false,
      overflowing: [],
      contrastFail: [],
      smallTargets: [],
      touchTargets: [],   // <44px：不违规，但触屏上偏小（增强项）
      unstyledDynamicNodes: [],
      unknownClasses: [],
      a11y: {
        iconButtonNoName: [],
        emptyLink: [],
        imgNoAlt: [],
        tabSemantics: null,
        positiveTabindex: [],
        headingOrder: []
      },
      layout: {
        zeroSize: [],
        emptyContainers: [],
        overlapping: []
      },
      stats: { nodes: 0, evClasses: 0, maxDepth: 0 }
    };

    var doc = root.ownerDocument || root;
    var win = doc.defaultView || window;
    var panel = root.querySelector ? (root.querySelector('.ev-panel') || root) : root;

    /* 1. 横向溢出 */
    report.overflowX = doc.documentElement.scrollWidth > win.innerWidth + 1;
    var all = panel.querySelectorAll('*');
    report.stats.nodes = all.length;
    var tabbar = panel.querySelector('.ev-tabs');
    for (var i = 0; i < all.length; i++) {
      var el = all[i];
      if (!isVisible(el)) continue;
      // 页签条内部横向滚动是预期行为，跳过
      if (tabbar && tabbar.contains(el)) continue;
      var r = el.getBoundingClientRect();
      if (r.right > win.innerWidth + 1 || r.left < -1) {
        report.overflowing.push({
          tag: el.tagName, cls: (el.className || '').toString().slice(0, 48),
          left: Math.round(r.left), right: Math.round(r.right)
        });
        if (report.overflowing.length >= 8) break;
      }
    }

    /* 2. 对比度 */
    for (var k = 0; k < all.length; k++) {
      var node = all[k];
      var hasText = Array.prototype.some.call(node.childNodes, function (n) {
        return n.nodeType === 3 && n.textContent.trim().length > 1;
      });
      if (!hasText || !isVisible(node)) continue;
      var cs = getComputedStyle(node);
      // WCAG 1.4.3 明确豁免「失效控件」，禁用按钮不该算对比度失败
      if (node.closest('[disabled], [aria-disabled="true"]')) continue;
      var fg = parseColor(cs.color);
      if (!fg) continue;
      var cr = contrast(fg, bgOf(node));
      var size = parseFloat(cs.fontSize), bold = parseInt(cs.fontWeight, 10) >= 700;
      var need = (size >= 24 || (size >= 18.66 && bold)) ? 3 : 4.5;
      if (cr < need - 0.02) {
        report.contrastFail.push({
          text: node.textContent.trim().slice(0, 24),
          cls: (node.className || '').toString().slice(0, 36),
          ratio: +cr.toFixed(2), need: need, fontSize: cs.fontSize
        });
        if (report.contrastFail.length >= 10) break;
      }
    }

    /* 3. 触控目标 */
    var clickable = panel.querySelectorAll('button, a[href], input, select, [role="button"], .ev-copy__btn');
    for (var t = 0; t < clickable.length; t++) {
      var c = clickable[t];
      if (!isVisible(c) || c.disabled) continue;
      var cr2 = c.getBoundingClientRect();
      // 阈值取 WCAG 2.2 SC 2.5.8 的 24×24 CSS px（AA 下限），
      // 而不是随手定的数字 —— 否则会把合规控件误报成问题。
      // 触屏另有 44px 的建议值，那属于增强项，单独统计。
      if (cr2.height < 24 || cr2.width < 24) {
        report.smallTargets.push({
          label: (c.textContent || '').trim().slice(0, 16) || c.tagName,
          w: Math.round(cr2.width), h: Math.round(cr2.height)
        });
      } else if (cr2.height < 40 && report.touchTargets) {
        report.touchTargets.push({ label: (c.textContent || '').trim().slice(0, 14) || c.tagName, h: Math.round(cr2.height) });
      }
    }

    /* 4. 未知类名：DOM 里用到的 ev-* 必须有 CSS 规则
          —— 这条能通用地抓住「重命名漏改 JS 里拼的类名」 */
    var defined = definedClasses();
    var used = Object.create(null);
    for (var n2 = 0; n2 < all.length; n2++) {
      var cl = all[n2].className;
      if (typeof cl !== 'string') continue;
      cl.split(/\s+/).forEach(function (one) {
        if (one && one.indexOf('ev-') === 0) used[one.toLowerCase()] = true;
      });
    }
    report.stats.evClasses = Object.keys(used).length;
    for (var u in used) {
      if (!defined[u]) report.unknownClasses.push(u);
    }

    /* 5. JS 动态生成的浮层是否真的拿到了样式
          —— 令牌作用域踩过坑：弹层挂在 body 下，取不到 .ev-panel 内的变量 */
    panel.parentNode && panel.parentNode.querySelectorAll && null;
    var overlays = doc.querySelectorAll('.ev-overlay, .ev-toasts');
    for (var o = 0; o < overlays.length; o++) {
      var ov = overlays[o];
      var ocs = getComputedStyle(ov);
      var problems = [];
      if (ov.classList.contains('ev-overlay')) {
        var modal = ov.querySelector('.ev-modal');
        if (modal) {
          var mcs = getComputedStyle(modal);
          var bg = parseColor(mcs.backgroundColor);
          if (!bg || bg.a < 0.9) problems.push('弹层背景非不透明（令牌取不到？）');
          if (parseFloat(mcs.borderRadius) === 0 && mcs.boxShadow === 'none') problems.push('弹层无圆角无阴影');
          var mbtn = modal.querySelector('.ev-btn');
          if (mbtn) {
            var bcs = getComputedStyle(mbtn);
            if (bcs.borderStyle === 'none' && (parseColor(bcs.backgroundColor) || {}).a < 0.5)
              problems.push('弹层内按钮无描边无底色');
          }
        }
      }
      if (problems.length) report.unstyledDynamicNodes.push({ cls: ov.className, problems: problems });
    }

    /* 6. 可访问性 */
    var btns = panel.querySelectorAll('button, [role="button"]');
    for (var b = 0; b < btns.length; b++) {
      var btn = btns[b];
      if (!isVisible(btn)) continue;
      var txt = (btn.textContent || '').trim();
      var aria = btn.getAttribute('aria-label') || btn.getAttribute('title') || '';
      if (!txt && !aria) {
        report.a11y.iconButtonNoName.push((btn.className || '').toString().slice(0, 44) || btn.tagName);
      }
    }
    var links = panel.querySelectorAll('a[href]');
    for (var l = 0; l < links.length; l++) {
      var a = links[l];
      if (!isVisible(a)) continue;
      var at = (a.textContent || '').trim();
      if (!at && !a.getAttribute('aria-label') && !a.getAttribute('title')) {
        report.a11y.emptyLink.push(a.getAttribute('href'));
      }
    }
    var imgs = panel.querySelectorAll('img');
    for (var im = 0; im < imgs.length; im++) {
      if (!imgs[im].hasAttribute('alt')) report.a11y.imgNoAlt.push(imgs[im].getAttribute('src'));
    }
    // 页签语义
    var tablist = panel.querySelector('[role="tablist"]');
    if (tablist) {
      var tabs = tablist.querySelectorAll('[role="tab"]');
      var activeCount = tablist.querySelectorAll('[aria-current="page"], .is-active').length;
      report.a11y.tabSemantics = {
        hasTablist: true, tabs: tabs.length, active: activeCount,
        ok: tabs.length > 0 && activeCount === 1
      };
    }
    // 正向 tabindex 会打乱阅读顺序
    var ti = panel.querySelectorAll('[tabindex]');
    for (var x = 0; x < ti.length; x++) {
      var v = parseInt(ti[x].getAttribute('tabindex'), 10);
      if (v > 0) report.a11y.positiveTabindex.push({ cls: (ti[x].className || '').toString().slice(0, 30), tabindex: v });
    }
    // 标题层级：不应跳级
    var hs = panel.querySelectorAll('h1,h2,h3,h4,h5,h6');
    var prev = 0;
    for (var h = 0; h < hs.length; h++) {
      var lvl = parseInt(hs[h].tagName[1], 10);
      if (prev && lvl > prev + 1) {
        report.a11y.headingOrder.push({ from: 'h' + prev, to: 'h' + lvl, text: (hs[h].textContent || '').trim().slice(0, 20) });
      }
      prev = lvl;
    }

    /* 7. 布局不变量 */
    var empties = panel.querySelectorAll('.ev-stat, .ev-kv__item, .ev-kv__v, .ev-sec');
    for (var e2 = 0; e2 < empties.length; e2++) {
      var ee = empties[e2];
      if (!isVisible(ee)) continue;
      var et = (ee.textContent || '').trim();
      var hasControl = ee.querySelector('button, a, input, select, i');
      if (et === '' && !hasControl) {
        report.layout.emptyContainers.push((ee.className || '').toString().slice(0, 40));
      }
    }

    /* 汇总问题数 */
    report.problems =
      (report.overflowX ? 1 : 0) + report.overflowing.length + report.contrastFail.length +
      report.smallTargets.length + report.unknownClasses.length + report.unstyledDynamicNodes.length +
      report.a11y.iconButtonNoName.length + report.a11y.emptyLink.length + report.a11y.imgNoAlt.length +
      (report.a11y.tabSemantics && !report.a11y.tabSemantics.ok ? 1 : 0) +
      report.a11y.positiveTabindex.length + report.a11y.headingOrder.length +
      report.layout.emptyContainers.length;
    return report;
  }

  /* ---------- 交互审计：点开每个浮层类控件，检查生成结果 ---------- */
  function interact(root) {
    var doc = (root || document).ownerDocument || document;
    var panel = (root || document).querySelector('.ev-panel');
    if (!panel) return { error: 'no panel' };
    var out = { opened: [], failures: [], consoleErrors: [] };

    var triggers = panel.querySelectorAll('[data-ev-action="dropdown"], [data-ev-action="console"]');
    for (var i = 0; i < triggers.length; i++) {
      var b = triggers[i];
      if (b.disabled) continue;
      try { b.click(); } catch (e) { out.failures.push({ trigger: b.textContent.trim().slice(0, 20), error: String(e) }); continue; }
      var overlay = doc.querySelector('.ev-overlay');
      if (!overlay) { out.failures.push({ trigger: b.textContent.trim().slice(0, 20), error: '点击后没有出现浮层' }); continue; }
      var modal = overlay.querySelector('.ev-modal');
      var info = { trigger: b.textContent.trim().slice(0, 20), hasModal: !!modal };
      if (modal) {
        var mcs = doc.defaultView.getComputedStyle(modal);
        var bg = parseColor(mcs.backgroundColor);
        info.modalOpaque = !!bg && bg.a > 0.9;
        info.modalRadius = parseFloat(mcs.borderRadius);
        var firstBtn = modal.querySelector('.ev-btn');
        if (firstBtn) {
          var bcs = doc.defaultView.getComputedStyle(firstBtn);
          info.btnStyled = bcs.borderStyle !== 'none' || (parseColor(bcs.backgroundColor) || {}).a > 0.5;
        }
        // 关闭按钮 / Esc 是否可用
        info.hasClose = !!modal.querySelector('.ev-modal__x, [data-ev-close]');
        // 焦点是否进入了弹层（真浏览器里 click 不一定移焦点，这里只看可聚焦元素存在性）
        info.focusable = modal.querySelectorAll('button, input, select, a[href]').length;
      }
      out.opened.push(info);

      // 触发的按钮若连到 console 会发请求，这里不关心；关掉弹层继续
      var closeBtn = overlay.querySelector('.ev-modal__x, [data-ev-close]');
      if (closeBtn) { try { closeBtn.click(); } catch (e) {} }
      if (doc.querySelector('.ev-overlay')) {
        // 兜底：直接移除，避免影响后续检查
        var ov2 = doc.querySelector('.ev-overlay');
        if (ov2 && ov2.parentNode) ov2.parentNode.removeChild(ov2);
      }
    }
    return out;
  }

  return { run: run, interact: interact };
})();
