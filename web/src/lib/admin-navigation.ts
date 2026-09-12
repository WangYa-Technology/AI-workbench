import {
  Activity,
  BriefcaseBusiness,
  CircleDollarSign,
  FileCheck2,
  FileKey2,
  Gauge,
  Headphones,
  KeyRound,
  ListFilter,
  Settings2,
  ShieldAlert,
  ShieldCheck,
  SlidersHorizontal,
  Users,
  WandSparkles,
  type LucideIcon,
} from 'lucide-vue-next'

export type AdminTab = 'overview' | 'users' | 'content' | 'media' | 'governance' | 'support' | 'generations' | 'tasks' | 'taskTypes' | 'providers' | 'models' | 'settings' | 'developer' | 'finance' | 'risk' | 'riskRules' | 'ranking' | 'dataRights' | 'diagnostics'

export type AdminNavigationItem = {
  tab: AdminTab
  permission: string
  icon: LucideIcon
}

export const adminNavigationItems: AdminNavigationItem[] = [
  { tab: 'overview', permission: 'admin:overview', icon: Gauge },
  { tab: 'users', permission: 'admin:users', icon: Users },
  { tab: 'content', permission: 'admin:content', icon: FileCheck2 },
  { tab: 'media', permission: 'admin:media', icon: ShieldCheck },
  { tab: 'generations', permission: 'admin:generations', icon: WandSparkles },
  { tab: 'governance', permission: 'admin:governance', icon: ShieldAlert },
  { tab: 'support', permission: 'admin:support', icon: Headphones },
  { tab: 'tasks', permission: 'admin:tasks', icon: BriefcaseBusiness },
  { tab: 'taskTypes', permission: 'admin:tasks', icon: ListFilter },
  { tab: 'providers', permission: 'admin:providers', icon: SlidersHorizontal },
  { tab: 'models', permission: 'admin:models', icon: WandSparkles },
  { tab: 'settings', permission: 'admin:settings', icon: Settings2 },
  { tab: 'developer', permission: 'admin:developer', icon: KeyRound },
  { tab: 'finance', permission: 'admin:finance', icon: CircleDollarSign },
  { tab: 'risk', permission: 'admin:risk', icon: Activity },
  { tab: 'riskRules', permission: 'admin:risk_rules', icon: Settings2 },
  { tab: 'ranking', permission: 'admin:ranking', icon: ListFilter },
  { tab: 'dataRights', permission: 'admin:data-rights', icon: FileKey2 },
  { tab: 'diagnostics', permission: 'admin:observability', icon: Activity },
]
