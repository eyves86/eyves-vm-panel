{* SSH 密钥：面板侧为账号级密钥库，这里只读展示，新增需在面板操作 *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.sshkey}</span>
</div>

{if $data.keys}
<div class="ev-tablewrap"><table class="ev-table">
  <thead>
    <tr>
      <th>{$lang.instance}</th>
      <th>{$lang.description}</th>
      <th>Fingerprint</th>
      <th>{$lang.created}</th>
    </tr>
  </thead>
  <tbody>
    {foreach $data.keys as $k}
      <tr>
        <td data-label="{$lang.instance}"><b>{$k.name|escape:'html'|default:'—'}</b></td>
        <td data-label="{$lang.description}">{$k.description|escape:'html'|default:'—'}</td>
        <td data-label="Fingerprint" class="is-mono">
          {$k.fingerprint|escape:'html'|truncate:32:'…'}
        </td>
        <td data-label="{$lang.created}" class="is-mono">{$k.created_at|default:'—'}</td>
      </tr>
    {/foreach}
  </tbody>
</table></div>
<div style="margin-top:14px;font-size:12px;color:var(--ev-ink-3)">
  {$lang.sshkey_managed_by_panel}
</div>
{else}
  <div class="ev-empty"><i class="bi bi-key"></i><span>{$lang.nosshkey}</span></div>
{/if}
