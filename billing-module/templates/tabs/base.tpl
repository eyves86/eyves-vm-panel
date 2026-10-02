{* ==========================================================================
   概览
   刻意不复述头部已有的信息（规格、状态、端口、到期），只补这一页才看得到的。
   键值对用 Cloudscape 的 KeyValuePairs 形态：多列网格、标签在上值在下。
   ========================================================================== *}

{* ---------- 连接方式：给出可直接复制的命令，省掉用户自己拼 ---------- *}
{if $vm.primary_ip and $vm.ssh_port}
  <div class="ev-sec"><span class="ev-sec__t">{$lang.connection}</span></div>
  <div class="ev-kv">
    <div class="ev-kv__item ev-kv__item--full">
      <div class="ev-kv__k">SSH</div>
      <div class="ev-kv__v is-mono">
        <span class="ev-copy">
          <span>ssh root@{$vm.primary_ip|escape:'html'} -p {$vm.ssh_port}</span>
          <button type="button" class="ev-copy__btn"
                  data-ev-copy="ssh root@{$vm.primary_ip|escape:'html'} -p {$vm.ssh_port}"
                  title="{$lang.copy}" aria-label="{$lang.copy}"><i class="bi bi-clipboard"></i></button>
        </span>
      </div>
    </div>
    {if $caps.console_vnc and $vm.vnc_port}
      <div class="ev-kv__item">
        <div class="ev-kv__k">VNC</div>
        <div class="ev-kv__v is-mono">
          <span class="ev-copy">
            <span>{$vm.primary_ip|escape:'html'}:{$vm.vnc_port}</span>
            <button type="button" class="ev-copy__btn"
                    data-ev-copy="{$vm.primary_ip|escape:'html'}:{$vm.vnc_port}"
                    title="{$lang.copy}" aria-label="{$lang.copy}"><i class="bi bi-clipboard"></i></button>
          </span>
        </div>
      </div>
    {/if}
    {if $vm.domain_name}
      <div class="ev-kv__item">
        <div class="ev-kv__k">Domain</div>
        <div class="ev-kv__v is-mono">{$vm.domain_name|escape:'html'}</div>
      </div>
    {/if}
  </div>
{/if}

{* ---------- 网络地址 ---------- *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.network}</span></div>
<div class="ev-kv">
  <div class="ev-kv__item">
    <div class="ev-kv__k">{$lang.publicipv4}</div>
    <div class="ev-kv__v is-mono">
      {if $vm.public_ipv4}
        {foreach $vm.public_ipv4 as $ip}
          <div>{$ip|escape:'html'}</div>
        {/foreach}
      {else}
        <span style="color:var(--ev-ink-3)">—</span>
      {/if}
    </div>
  </div>
  <div class="ev-kv__item">
    <div class="ev-kv__k">{$lang.ipv6}</div>
    <div class="ev-kv__v is-mono">
      {if $vm.ipv6_addresses}
        {foreach $vm.ipv6_addresses as $ip}
          <div>{$ip|escape:'html'}</div>
        {/foreach}
      {else}
        <span style="color:var(--ev-ink-3)">—</span>
      {/if}
    </div>
  </div>
  <div class="ev-kv__item">
    <div class="ev-kv__k">MAC</div>
    <div class="ev-kv__v is-mono">{if $vm.mac_address}{$vm.mac_address|escape:'html'}{else}<span style="color:var(--ev-ink-3)">—</span>{/if}</div>
  </div>
</div>

{* ---------- 系统 ---------- *}
<div class="ev-sec"><span class="ev-sec__t">{$lang.system_info}</span></div>
<div class="ev-kv">
  <div class="ev-kv__item">
    <div class="ev-kv__k">{$lang.operatingsystem}</div>
    <div class="ev-kv__v">{$vm.template_id|escape:'html'|default:'—'}</div>
  </div>
  <div class="ev-kv__item">
    <div class="ev-kv__k">{$lang.runtime}</div>
    <div class="ev-kv__v">{$vm.runtime_label}</div>
  </div>
  <div class="ev-kv__item">
    <div class="ev-kv__k">{$lang.created}</div>
    <div class="ev-kv__v is-mono">{$vm.created_at|default:'—'}</div>
  </div>
  <div class="ev-kv__item">
    <div class="ev-kv__k">{$lang.owner}</div>
    <div class="ev-kv__v">{$vm.owner|escape:'html'|default:'—'}</div>
  </div>
  <div class="ev-kv__item">
    <div class="ev-kv__k">{$lang.node}</div>
    <div class="ev-kv__v">{$vm.node_name|escape:'html'|default:'—'}</div>
  </div>
  <div class="ev-kv__item">
    <div class="ev-kv__k">{$lang.remark}</div>
    <div class="ev-kv__v">{if $vm.remark}{$vm.remark|escape:'html'}{else}<span style="color:var(--ev-ink-3)">—</span>{/if}</div>
  </div>
</div>
