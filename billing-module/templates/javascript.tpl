{* ==========================================================================
   前端交互层
   所有请求走 api_client.php；分流逻辑（LXC→ssh / KVM→vnc）由后端决定，
   这里只消费后端回传的 effective_type，不自己判断运行时。
   ========================================================================== *}
<script>
(function () {
  'use strict';

  var PANEL   = document.getElementById('evPanel');
  if (!PANEL) { return; }

  var ENDPOINT = '{$api}';
  var SID      = '{$serviceid}';
  var LANG     = {$langJson};
  var PAGE     = '{$page}';

  /* ------------------------------------------------------------ 工具 */
  function toast(msg, type) {
    var wrap = document.querySelector('.ev-toasts');
    if (!wrap) {
      wrap = document.createElement('div');
      wrap.className = 'ev-toasts';
      document.body.appendChild(wrap);
    }
    var el = document.createElement('div');
    el.className = 'ev-toast' + (type ? ' ev-toast--' + type : '');
    el.textContent = msg;
    wrap.appendChild(el);
    setTimeout(function () { el.remove(); }, 3600);
  }

  function post(action, params) {
    var body = new URLSearchParams();
    body.append('serviceid', SID);
    body.append('action', action);
    Object.keys(params || {}).forEach(function (k) {
      var v = params[k];
      if (Array.isArray(v)) { v.forEach(function (item) { body.append(k + '[]', item); }); }
      else { body.append(k, v); }
    });
    return fetch(ENDPOINT, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString()
    }).then(function (r) { return r.json(); }).catch(function () {
      return { status: 'error', message: LANG.panel_unreachable, code: 'NETWORK' };
    });
  }

  function busy(btn, on) {
    if (!btn) { return; }
    if (on) { btn.dataset.evBusy = '1'; btn.disabled = true; }
    else { delete btn.dataset.evBusy; btn.disabled = false; }
  }

  /* ------------------------------------------------------------ 控制台 */
  function openConsole(btn) {
    busy(btn, true);
    post('getConsole').then(function (res) {
      busy(btn, false);
      if (res.status !== 'success') {
        // 后端在失败路径也会回传分流结果，用正确的措辞提示
        var d = res.data || {};
        var label = (d.console && d.console.label) || LANG.console;
        var msg = res.message || LANG.operation_failed;
        if (res.code === 'PRECONDITION_FAILED') {
          toast(msg, 'error');
        } else {
          toast(label + '：' + msg, 'error');
        }
        return;
      }
      var d = res.data || {};
      var url = d.url || '';
      if (!url) { toast(LANG.operation_failed, 'error'); return; }
      window.open(url, '_blank', 'noopener');
    });
  }

  /* ------------------------------------------------------------ 电源 */
  function runPower(op, label, btn) {
    busy(btn, true);
    post('power', { op: op }).then(function (res) {
      if (res.status === 'success') {
        toast((label || LANG.operation_success) + ' ✓', 'success');
        setTimeout(function () { location.reload(); }, 900);
      } else {
        busy(btn, false);
        toast(res.message || LANG.operation_failed, 'error');
      }
    });
  }

  /* ------------------------------------------------------------ 弹层 */
  function closeModal() {
    var m = document.querySelector('.ev-overlay');
    if (m) { m.remove(); }
  }

  function confirmModal(opts) {
    closeModal();
    var overlay = document.createElement('div');
    overlay.className = 'ev-overlay';
    overlay.innerHTML =
      '<div class="ev-modal" role="dialog" aria-modal="true">' +
        '<div class="ev-modal__head">' +
          '<h3 class="ev-modal__t"></h3>' +
          '<button type="button" class="ev-modal__x" data-ev-close>&times;</button>' +
        '</div>' +
        '<div class="ev-modal__b"></div>' +
        '<div class="ev-modal__f">' +
          '<button type="button" class="ev-btn" data-ev-close>' + LANG.cancel + '</button>' +
          '<button type="button" class="ev-btn ' + (opts.danger ? 'ev-btn--danger' : 'ev-btn--primary') + '" data-ev-confirm></button>' +
        '</div>' +
      '</div>';
    overlay.querySelector('.ev-modal__t').textContent = opts.title || LANG.actionconfirmtitle;
    overlay.querySelector('.ev-modal__b').innerHTML = opts.body || '';
    var okBtn = overlay.querySelector('[data-ev-confirm]');
    okBtn.textContent = opts.okText || LANG.confirm;

    overlay.addEventListener('click', function (e) {
      if (e.target === overlay || e.target.closest('[data-ev-close]')) { closeModal(); return; }
      if (e.target.closest('[data-ev-confirm]')) {
        if (opts.onConfirm) { opts.onConfirm(okBtn); }
        else { closeModal(); }
      }
    });
    document.body.appendChild(overlay);
    if (opts.afterRender) { opts.afterRender(overlay); }
  }

  /* ------------------------------------------------------------ 下拉菜单 */
  function openDropdown(anchor, key) {
    var menus = {
      power: {
        title: LANG.actionconfirmtitle,
        items: [
          { op: 'start',      label: LANG.boot },
          { op: 'shutdown',   label: LANG.shutdown },
          { op: 'hard-stop',  label: LANG.hardshutdown, danger: true }
        ]
      },
      reboot: {
        title: LANG.reboot,
        items: [
          { op: 'restart',      label: LANG.reboot },
          { op: 'hard-restart', label: LANG.hardreboot, danger: true }
        ]
      }
    };
    var menu = menus[key];
    if (!menu) { return; }

    var html = '<div class="ev-note ev-note--warn" style="margin-bottom:12px">' + LANG.warn_irreversible + '</div>';
    html += '<div class="ev-btncol">';
    menu.items.forEach(function (it) {
      // 动作列表里只有危险项着色：全实心会让人看不出该点哪个
      html += '<button type="button" class="ev-btn ' + (it.danger ? 'ev-btn--danger' : '') +
              ' ev-btn--block" style="justify-content:flex-start" data-op="' + it.op + '">' + it.label + '</button>';
    });
    html += '</div>';

    confirmModal({
      title: menu.title,
      body: html,
      okText: null,
      afterRender: function (overlay) {
        // 下拉菜单是三选一，不需要「确定」按钮
        var foot = overlay.querySelector('.ev-modal__f');
        var ok = foot.querySelector('[data-ev-confirm]');
        if (ok) { ok.remove(); }
        foot.querySelectorAll('[data-op]').forEach(function (b) { b.remove(); });
        overlay.querySelectorAll('[data-op]').forEach(function (b) {
          b.addEventListener('click', function () {
            closeModal();
            runPower(b.dataset.op, b.textContent.trim(), anchor);
          });
        });
      }
    });
  }

  /* ------------------------------------------------------------ 复制 */
  function copyText(text, el) {
    var done = function () {
      toast(LANG.copied, 'success');
      // 被复制的元素本身也要有反馈，而不是只有角落的 toast
      if (el) {
        el.classList.add('is-copied');
        el.setAttribute('data-ev-copied-label', LANG.copied);
        setTimeout(function () { el.classList.remove('is-copied'); }, 1200);
      }
    };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(done, function () { toast(LANG.operation_failed, 'error'); });
      return;
    }
    var ta = document.createElement('textarea');
    ta.value = text; ta.setAttribute('readonly', '');
    ta.style.position = 'fixed'; ta.style.top = '-1000px';
    document.body.appendChild(ta); ta.select();
    try { document.execCommand('copy'); done(); }
    catch (e) { toast(LANG.operation_failed, 'error'); }
    ta.remove();
  }

  /* ------------------------------------------------------------ 页签溢出提示 */
  function syncTabsOverflow() {
    var wrap = document.querySelector('[data-ev-tabswrap]');
    if (!wrap) { return; }
    var nav = wrap.querySelector('.ev-tabs');
    if (!nav) { return; }
    var overflowing = nav.scrollWidth - nav.clientWidth > 2;
    var atEnd = nav.scrollLeft + nav.clientWidth >= nav.scrollWidth - 2;
    wrap.classList.toggle('is-overflow', overflowing && !atEnd);
    wrap.classList.toggle('is-atend', atEnd);
    wrap.classList.toggle('is-scrolled', overflowing && nav.scrollLeft > 2);
    // 窄屏把当前页签滚进视野，避免「点了页签却看不到高亮」
    var active = nav.querySelector('.is-active');
    if (active && overflowing) {
      var left = active.offsetLeft - nav.clientWidth / 2 + active.offsetWidth / 2;
      nav.scrollLeft = Math.max(0, Math.min(left, nav.scrollWidth - nav.clientWidth));
    }
  }

  window.addEventListener('resize', syncTabsOverflow);
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', syncTabsOverflow);
  } else {
    syncTabsOverflow();
  }
  var tabsNav = document.querySelector('[data-ev-tabswrap] .ev-tabs');
  if (tabsNav) { tabsNav.addEventListener('scroll', syncTabsOverflow, { passive: true }); }

  /* ------------------------------------------------------------ 全局委托 */
  document.addEventListener('click', function (e) {
    var copyEl = e.target.closest('[data-ev-copy]');
    if (copyEl) { copyText(copyEl.dataset.evCopy); return; }

    var btn = e.target.closest('[data-ev-action]');
    if (!btn || btn.disabled) { return; }
    var act = btn.dataset.evAction;

    if (act === 'console') { openConsole(btn); return; }
    if (act === 'dropdown') { openDropdown(btn, btn.dataset.evMenu); return; }
    if (act === 'close') { closeModal(); return; }
    if (act === 'refresh') { location.reload(); return; }
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') { closeModal(); }
  });

  /* ------------------------------------------------------------ 对外 API
     供各页签模板内联脚本调用，避免每页重复实现 */
  window.EvClient = {
    post: post,
    toast: toast,
    busy: busy,
    confirmModal: confirmModal,
    closeModal: closeModal,
    openConsole: openConsole,
    runPower: runPower,
    lang: LANG,
    serviceId: SID
  };

  /* 统一处理「危险操作 → 输入实例名确认」的模式（重装等） */
  window.EvClient.confirmByName = function (opts) {
    var html =
      '<div class="ev-note ev-note--danger">' + (opts.warning || '') + '</div>' +
      '<div class="ev-field">' +
        '<label>' + (opts.label || LANG.reinstall_confirm) + '</label>' +
        '<input type="text" class="ev-input" data-ev-nameinput placeholder="' + (opts.expect || '') + '" autocomplete="off">' +
      '</div>';
    confirmModal({
      title: opts.title,
      body: html,
      okText: opts.okText || LANG.confirm,
      danger: true,
      afterRender: function (overlay) {
        overlay.querySelector('[data-ev-nameinput]').focus();
      },
      onConfirm: function (btn) {
        var val = document.querySelector('[data-ev-nameinput]').value.trim();
        if (val !== opts.expect) { toast(LANG.reinstall_name_mismatch, 'error'); return; }
        closeModal();
        opts.onPass(btn);
      }
    });
  };
})();
</script>
