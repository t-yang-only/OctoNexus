import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { Bell, Database, KeyRound, Scale, SlidersHorizontal } from 'lucide-react';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { SettingAppearance } from './Appearance';
import { SettingSystem } from './System';
import { SettingRouting } from './Routing';
import { SettingModelMapping } from './ModelMapping';
import { SettingNotify } from './Notify';
import { SettingUsageReport } from './UsageReport';
import { SettingAlertRule } from './AlertRule';
import { SettingAPIKey } from './APIKey';
import { SettingCLIExport } from './CLIExport';
import { SettingLLMPrice } from './LLMPrice';
import { SettingAccount } from './Account';
import { SettingInfo } from './Info';
import { SettingLog } from './Log';
import { SettingBackup } from './Backup';
import { SettingWebDAVBackup } from './WebDAVBackup';

// 设置页的功能分区：图标 + i18n 键。分区顺序按「日常改动频率」排，越靠前越常改。
const TABS = [
    { key: 'general', icon: SlidersHorizontal },
    { key: 'api', icon: KeyRound },
    { key: 'routing', icon: Scale },
    { key: 'notify', icon: Bell },
    { key: 'data', icon: Database },
] as const;

// Setting 渲染设置页面正文。按功能区切成五个独立分区，
// 取代此前把 8 张卡片平铺进瀑布流的做法——找一项设置不必再在长页面里来回滚。
export function Setting() {
    const t = useTranslations('setting');
    const [tab, setTab] = useState<string>('general');

    return (
        <div className="h-full min-h-0 overflow-y-auto overscroll-contain rounded-t-3xl pb-24 md:pb-4">
            <Tabs value={tab} onValueChange={setTab} className="gap-4">
                {/* 吸顶：滚动长分区时分区切换始终在视野内；背景加一层毛玻璃，避免内容从下方透出来。 */}
                <TabsList className="sticky top-0 z-10 mx-4 mt-4 border border-border/60 bg-background/80 backdrop-blur-md">
                    {TABS.map(({ key, icon: Icon }) => (
                        <TabsTrigger key={key} value={key} className="gap-1.5">
                            <Icon className="size-3.5" />
                            {t(`tabs.${key}`)}
                        </TabsTrigger>
                    ))}
                </TabsList>
                <TabsContent value="general" className="space-y-4 px-4">
                    <SettingInfo />
                    <SettingAppearance />
                    <SettingSystem />
                </TabsContent>
                <TabsContent value="api" className="px-4">
                    <SettingAPIKey />
                    <SettingCLIExport />
                </TabsContent>
                <TabsContent value="routing" className="space-y-4 px-4">
                    <SettingModelMapping />
                    <SettingRouting />
                </TabsContent>
                <TabsContent value="notify" className="space-y-4 px-4">
                    <SettingNotify />
                    <SettingUsageReport />
                    <SettingAlertRule />
                </TabsContent>
                <TabsContent value="data" className="space-y-4 px-4">
                    <SettingAccount />
                    <SettingLog />
                    <SettingLLMPrice />
                    <SettingBackup />
                    <SettingWebDAVBackup />
                </TabsContent>
            </Tabs>
        </div>
    );
}
