{* 安全组：勾选关联/解除。两种运行时通用。 *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.securitys}</span>
  <button type="button" class="ev-btn ev-btn--sm ev-btn--primary" data-ev-sgsave
          {if $vm.suspended || $vm.locked}disabled{/if}>{$lang.save}</button>
</div>

{if $vm.suspended}<div class="ev-note ev-note--warn">{$lang.suspended_tip}</div>{/if}

{if $data.groups}
<div class="ev-tablewrap"><table class="ev-table">
  <thead>
    <tr>
      <th style="width:56px">{$lang.group_attached}</th>
      <th>{$lang.security_group}</th>
      <th>{$lang.description}</th>
      <th>{$data.ruleLabel|default:'Rules'}</th>
    </tr>
  </thead>
  <tbody>
    {foreach $data.groups as $g}
      <tr>
        <td class="ev-sel">
          <input type="checkbox" data-ev-group="{$g.id|escape:'html'}"
                 {if in_array($g.id, $data.attached)}checked{/if}
                 {if $vm.suspended || $vm.locked}disabled{/if}>
        </td>
        <td data-label="{$lang.security_group}"><b>{$g.name|escape:'html'}</b></td>
        <td data-label="{$lang.description}">{$g.description|escape:'html'|default:'—'}</td>
        <td data-label="{$lang.action}" class="is-mono">{if isset($g.rule_count)}{$g.rule_count}{else}—{/if}</td>
      </tr>
    {/foreach}
  </tbody>
</table></div>
{else}
  <div class="ev-empty"><i class="bi bi-shield-check"></i><span>{$lang.no_data}</span></div>
{/if}

<script>
(function () {
  var save = document.querySelector('[data-ev-sgsave]');
  if (!save) { return; }
  save.addEventListener('click', function () {
    var ids = [];
    document.querySelectorAll('[data-ev-group]').forEach(function (c) { if (c.checked) { ids.push(c.dataset.evGroup); } });
    EvClient.busy(save, true);
    EvClient.post('attachSecurityGroups', { groups: ids }).then(function (res) {
      EvClient.busy(save, false);
      EvClient.toast(res.message || (res.status === 'success' ? EvClient.lang.operation_success : EvClient.lang.operation_failed),
                     res.status === 'success' ? 'success' : 'error');
    });
  });
})();
</script>
