// 区域国家/地区字典：code 为 ISO 3166-1 alpha-2（小写，与 flag-icons 文件名一致）。
// 后端 region.country 存大写，前端展示时统一转小写取图。
export interface CountryOption {
  code: string
  zh: string
  en: string
  group: CountryGroup
}

export type CountryGroup = 'asia' | 'europe' | 'north-america' | 'south-america' | 'oceania' | 'middle-east' | 'africa'

export const COUNTRY_GROUP_LABELS: Record<CountryGroup, { zh: string; en: string }> = {
  asia: { zh: '亚洲', en: 'Asia' },
  europe: { zh: '欧洲', en: 'Europe' },
  'north-america': { zh: '北美洲', en: 'North America' },
  'south-america': { zh: '南美洲', en: 'South America' },
  oceania: { zh: '大洋洲', en: 'Oceania' },
  'middle-east': { zh: '中东', en: 'Middle East' },
  africa: { zh: '非洲', en: 'Africa' },
}

const RAW: Array<[string, string, string, CountryGroup]> = [
  // 亚洲
  ['cn', '中国大陆', 'China', 'asia'],
  ['hk', '中国香港', 'Hong Kong', 'asia'],
  ['mo', '中国澳门', 'Macao', 'asia'],
  ['tw', '中国台湾', 'Taiwan', 'asia'],
  ['jp', '日本', 'Japan', 'asia'],
  ['kr', '韩国', 'South Korea', 'asia'],
  ['sg', '新加坡', 'Singapore', 'asia'],
  ['my', '马来西亚', 'Malaysia', 'asia'],
  ['id', '印度尼西亚', 'Indonesia', 'asia'],
  ['th', '泰国', 'Thailand', 'asia'],
  ['vn', '越南', 'Vietnam', 'asia'],
  ['ph', '菲律宾', 'Philippines', 'asia'],
  ['in', '印度', 'India', 'asia'],
  ['pk', '巴基斯坦', 'Pakistan', 'asia'],
  ['bd', '孟加拉国', 'Bangladesh', 'asia'],
  ['lk', '斯里兰卡', 'Sri Lanka', 'asia'],
  ['np', '尼泊尔', 'Nepal', 'asia'],
  ['mm', '缅甸', 'Myanmar', 'asia'],
  ['kh', '柬埔寨', 'Cambodia', 'asia'],
  ['la', '老挝', 'Laos', 'asia'],
  ['mn', '蒙古', 'Mongolia', 'asia'],
  ['kz', '哈萨克斯坦', 'Kazakhstan', 'asia'],
  ['uz', '乌兹别克斯坦', 'Uzbekistan', 'asia'],
  // 欧洲
  ['gb', '英国', 'United Kingdom', 'europe'],
  ['de', '德国', 'Germany', 'europe'],
  ['fr', '法国', 'France', 'europe'],
  ['nl', '荷兰', 'Netherlands', 'europe'],
  ['be', '比利时', 'Belgium', 'europe'],
  ['lu', '卢森堡', 'Luxembourg', 'europe'],
  ['ch', '瑞士', 'Switzerland', 'europe'],
  ['at', '奥地利', 'Austria', 'europe'],
  ['it', '意大利', 'Italy', 'europe'],
  ['es', '西班牙', 'Spain', 'europe'],
  ['pt', '葡萄牙', 'Portugal', 'europe'],
  ['ie', '爱尔兰', 'Ireland', 'europe'],
  ['se', '瑞典', 'Sweden', 'europe'],
  ['no', '挪威', 'Norway', 'europe'],
  ['dk', '丹麦', 'Denmark', 'europe'],
  ['fi', '芬兰', 'Finland', 'europe'],
  ['is', '冰岛', 'Iceland', 'europe'],
  ['pl', '波兰', 'Poland', 'europe'],
  ['cz', '捷克', 'Czechia', 'europe'],
  ['sk', '斯洛伐克', 'Slovakia', 'europe'],
  ['hu', '匈牙利', 'Hungary', 'europe'],
  ['ro', '罗马尼亚', 'Romania', 'europe'],
  ['bg', '保加利亚', 'Bulgaria', 'europe'],
  ['gr', '希腊', 'Greece', 'europe'],
  ['ru', '俄罗斯', 'Russia', 'europe'],
  ['ua', '乌克兰', 'Ukraine', 'europe'],
  ['by', '白俄罗斯', 'Belarus', 'europe'],
  ['lt', '立陶宛', 'Lithuania', 'europe'],
  ['lv', '拉脱维亚', 'Latvia', 'europe'],
  ['ee', '爱沙尼亚', 'Estonia', 'europe'],
  ['mt', '马耳他', 'Malta', 'europe'],
  ['cy', '塞浦路斯', 'Cyprus', 'europe'],
  // 北美洲
  ['us', '美国', 'United States', 'north-america'],
  ['ca', '加拿大', 'Canada', 'north-america'],
  ['mx', '墨西哥', 'Mexico', 'north-america'],
  ['cr', '哥斯达黎加', 'Costa Rica', 'north-america'],
  ['pa', '巴拿马', 'Panama', 'north-america'],
  ['jm', '牙买加', 'Jamaica', 'north-america'],
  // 南美洲
  ['br', '巴西', 'Brazil', 'south-america'],
  ['ar', '阿根廷', 'Argentina', 'south-america'],
  ['cl', '智利', 'Chile', 'south-america'],
  ['co', '哥伦比亚', 'Colombia', 'south-america'],
  ['pe', '秘鲁', 'Peru', 'south-america'],
  ['uy', '乌拉圭', 'Uruguay', 'south-america'],
  // 大洋洲
  ['au', '澳大利亚', 'Australia', 'oceania'],
  ['nz', '新西兰', 'New Zealand', 'oceania'],
  ['fj', '斐济', 'Fiji', 'oceania'],
  // 中东
  ['ae', '阿联酋', 'United Arab Emirates', 'middle-east'],
  ['sa', '沙特阿拉伯', 'Saudi Arabia', 'middle-east'],
  ['qa', '卡塔尔', 'Qatar', 'middle-east'],
  ['kw', '科威特', 'Kuwait', 'middle-east'],
  ['bh', '巴林', 'Bahrain', 'middle-east'],
  ['om', '阿曼', 'Oman', 'middle-east'],
  ['tr', '土耳其', 'Türkiye', 'middle-east'],
  ['il', '以色列', 'Israel', 'middle-east'],
  // 非洲
  ['za', '南非', 'South Africa', 'africa'],
  ['eg', '埃及', 'Egypt', 'africa'],
  ['ng', '尼日利亚', 'Nigeria', 'africa'],
  ['ke', '肯尼亚', 'Kenya', 'africa'],
  ['ma', '摩洛哥', 'Morocco', 'africa'],
  ['tn', '突尼斯', 'Tunisia', 'africa'],
  ['gh', '加纳', 'Ghana', 'africa'],
  ['tz', '坦桑尼亚', 'Tanzania', 'africa'],
  ['mu', '毛里求斯', 'Mauritius', 'africa'],
]

export const COUNTRIES: CountryOption[] = RAW.map(([code, zh, en, group]) => ({ code, zh, en, group }))

const BY_CODE = new Map(COUNTRIES.map((item) => [item.code, item]))

/** countryLabel 返回国家/地区展示名；未知代码回退为原代码大写。 */
export function countryLabel(code: string | undefined, language: string): string {
  const item = BY_CODE.get((code || '').trim().toLowerCase())
  if (!item) return (code || '').trim().toUpperCase()
  return language === 'en' ? item.en : item.zh
}

/** groupedCountries 按大洲分组，供 <optgroup> 渲染。 */
export function groupedCountries(language: string): Array<{ group: CountryGroup; label: string; items: Array<{ code: string; label: string }> }> {
  const order: CountryGroup[] = ['asia', 'europe', 'north-america', 'south-america', 'oceania', 'middle-east', 'africa']
  return order.map((group) => ({
    group,
    label: language === 'en' ? COUNTRY_GROUP_LABELS[group].en : COUNTRY_GROUP_LABELS[group].zh,
    items: COUNTRIES.filter((item) => item.group === group).map((item) => ({
      code: item.code,
      label: language === 'en' ? item.en : item.zh,
    })),
  }))
}
