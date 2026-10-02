{* 端口映射（NAT）。配额用尽或实例挂起时禁用新增。 *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.portmap}</span>
  <div class="ev-btnrow">
    <span style="font-size:12px;color:var(--ev-ink-2)">
      {$lang.port_used} <b style="font-family:var(--ev-mono)">{$data.used}</b>
      {if $data.limit gt 0} / {$lang.port_limit} {$data.limit}{/if}
    </span>
    <button type="button" class="ev-btn ev-btn--sm ev-btn--primary" data-ev-portadd
            {if $vm.suspended || ($data.limit gt 0 && $data.used ge $data.limit)}disabled{/if}>
      <i class="bi bi-plus-lg"></i> {$lang.port_add}
    </button>
  </div>
</div>

{if $vm.suspended}
  <div class="ev-note ev-note--warn">{$lang.suspended_tip}</div>
{/if}

{if $data.ports}
<div class="ev-tablewrap"><table class="ev-table">
  <thead>
    <tr>
      <th>{$lang.external_port}</th>
      <th>{$lang.internal_port}</th>
      <th>{$lang.protocol}</th>
      <th>{$lang.remark}</th>
      <th class="ev-r">{$lang.action}</th>
    </tr>
  </thead>
  <tbody>
    {foreach $data.ports as $p}
      <tr>
        <td data-label="{$lang.external_port}" class="is-mono">{$p.external_port}</td>
        <td data-label="{$lang.internal_port}" class="is-mono">{$p.internal_port}</td>
        <td data-label="{$lang.protocol}"><span class="ev-chip ev-chip--ghost">{$p.protocol|upper}</span></td>
        <td data-label="{$lang.remark}">{$p.remark|escape:'html'|default:'—'}</td>
        <td data-label="{$lang.action}" class="ev-r">
          <button type="button" class="ev-btn ev-btn--sm ev-btn--danger"
                  data-ev-portdel="{$p.index}" {if $vm.suspended}disabled{/if}>{$lang.delete}</button>
        </td>
      </tr>
    {/foreach}
  </tbody>
</table></div>
{else}
  <div class="ev-empty"><i class="bi bi-shuffle"></i><span>{$lang.no_portmap}</span></div>
{/if}

<script>
(function () {
  function askRandom() {
    return EvClient.post('randomPort').then(function (res) {
      return (res.status === 'success' && res.data && res.data.port) ? res.data.port : '';
    }).catch(function () { return ''; });
  }

  document.querySelectorAll('[data-ev-portdel]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      if (!confirm(EvClient.lang.confirm_delete)) { return; }
      EvClient.busy(btn, true);
      EvClient.post('deletePortMapping', { index: btn.dataset.evPortdel }).then(function (res) {
        if (res.status === 'success') { location.reload(); }
        else { EvClient.busy(btn, false); EvClient.toast(res.message, 'error'); }
      });
    });
  });

  var addBtn = document.querySelector('[data-ev-portadd]');
  if (addBtn) {
    addBtn.addEventListener('click', function () {
      var html =
        '<div class="ev-row2">' +
          '<div class="ev-field"><label>' + EvClient.lang.external_port + '</label>' +
            '<input type="number" class="ev-input" data-ev-p="external_port" min="1" max="65535" placeholder="' + EvClient.lang.random_port + '"></div>' +
          '<div class="ev-field"><label>' + EvClient.lang.internal_port + '</label>' +
            '<input type="number" class="ev-input" data-ev-p="internal_port" min="1" max="65535"></div>' +
        '</div>' +
        '<div class="ev-row2">' +
          '<div class="ev-field"><label>' + EvClient.lang.protocol + '</label>' +
            '<select class="ev-select" data-ev-p="protocol"><option value="tcp">TCP</option><option value="udp">UDP</option></select></div>' +
          '<div class="ev-field"><label>' + EvClient.lang.remark + '</label>' +
            '<input type="text" class="ev-input" data-ev-p="remark"></div>' +
        '</div>';
      EvClient.confirmModal({
        title: EvClient.lang.port_add,
        body: html,
        okText: EvClient.lang.submit,
        afterRender: function (overlay) {
          // 外部端口留空时自动取一个随机可用端口
          askRandom().then(function (port) {
            var input = overlay.querySelector('[data-ev-p="external_port"]');
            if (port && input && !input.value) { input.placeholder = port; input.dataset.evRandom = port; }
          });
        },
        onConfirm: function (btn) {
          var params = {};
          document.querySelectorAll('[data-ev-p]').forEach(function (el) {
            params[el.dataset.evP] = el.dataset.evRandom && el.dataset.evP === 'external_port' && !el.value
              ? el.dataset.evRandom : el.value;
          });
          if (!params.internal_port) { EvClient.toast(EvClient.lang.internal_port + ' ?', 'error'); return; }
          EvClient.busy(btn, true);
          EvClient.post('addPortMapping', params).then(function (res) {
            if (res.status === 'success') { location.reload(); }
            else { EvClient.busy(btn, false); EvClient.toast(res.message, 'error'); }
          });
        }
      });
    });
  }
})();
</script>
