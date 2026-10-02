{* ISO 与救援 —— 仅 KVM 实例会渲染到这个页签（导航由 caps.iso_mount 控制） *}
<div class="ev-note ev-note--info">
  <i class="bi bi-info-circle"></i>
  <div>{$lang.iso_kvm_only}</div>
</div>

{if $vm.suspended}<div class="ev-note ev-note--warn">{$lang.suspended_tip}</div>{/if}

{* 救援模式状态 *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.rescue_mode}</span>
  {if $vm.rescue_enabled}
    <span class="ev-chip ev-chip--running">{$lang.rescue_enabled}</span>
  {/if}
</div>

<div class="ev-btnrow" style="margin-bottom:22px">
  {if $vm.rescue_enabled}
    <button type="button" class="ev-btn ev-btn--danger" data-ev-rescue="exit"
            {if $vm.suspended || $vm.locked}disabled{/if}>{$lang.rescue_exit}</button>
  {else}
    <button type="button" class="ev-btn ev-btn--primary" data-ev-rescue="enter"
            {if $vm.suspended || $vm.locked}disabled{/if}>{$lang.rescue_enter}</button>
  {/if}
</div>

{* ISO 列表 *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.iso_mount}</span>
</div>

{if $data.isos}
<div class="ev-tablewrap"><table class="ev-table">
  <thead>
    <tr>
      <th>ISO ID</th>
      <th>{$lang.description}</th>
      <th>{$lang.action}</th>
    </tr>
  </thead>
  <tbody>
    {foreach $data.isos as $iso}
      <tr>
        <td data-label="ISO ID" class="is-mono">{$iso.id|escape:'html'}</td>
        <td data-label="{$lang.description}">{$iso.name|escape:'html'|default:$iso.path|escape:'html'}</td>
        <td data-label="{$lang.action}">
          <button type="button" class="ev-btn ev-btn--sm" data-ev-iso="{$iso.id|escape:'html'}"
                  {if $vm.suspended || $vm.locked}disabled{/if}>{$lang.rescue_enter}</button>
        </td>
      </tr>
    {/foreach}
  </tbody>
</table></div>
{else}
  <div class="ev-empty"><i class="bi bi-disc"></i><span>{$lang.no_data}</span></div>
{/if}

<script>
(function () {
  document.querySelectorAll('[data-ev-rescue]').forEach(function (b) {
    b.addEventListener('click', function () {
      var mode = b.dataset.evRescue;
      var done = function (btn) {
        EvClient.busy(btn, true);
        EvClient.post(mode === 'enter' ? 'rescueEnter' : 'rescueExit', {}).then(function (res) {
          if (res.status === 'success') { location.reload(); }
          else { EvClient.busy(btn, false); EvClient.toast(res.message, 'error'); }
        });
      };
      if (mode === 'exit') { done(b); return; }
      EvClient.confirmModal({
        title: EvClient.lang.rescue_enter,
        body: '<div class="ev-note ev-note--danger">' + EvClient.lang.warn_irreversible + '</div>' +
              '<div style="font-size:13px">' + EvClient.lang.rescue_pick_iso + '</div>',
        okText: EvClient.lang.close, danger: true
      });
    });
  });

  document.querySelectorAll('[data-ev-iso]').forEach(function (b) {
    b.addEventListener('click', function () {
      EvClient.confirmModal({
        title: EvClient.lang.rescue_enter,
        body: '<div class="ev-note ev-note--danger">' + EvClient.lang.warn_irreversible + '</div>',
        okText: EvClient.lang.confirm, danger: true,
        onConfirm: function (btn) {
          EvClient.busy(btn, true);
          EvClient.post('rescueEnter', { iso_id: b.dataset.evIso }).then(function (res) {
            if (res.status === 'success') { location.reload(); }
            else { EvClient.busy(btn, false); EvClient.toast(res.message, 'error'); }
          });
        }
      });
    });
  });
})();
</script>
