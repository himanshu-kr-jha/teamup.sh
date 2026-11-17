import { useState } from 'react';
import { FolderGit2, Plus } from 'lucide-react';
import { Link } from 'react-router-dom';
import { Button } from '@/shared/components/ui/button';
import { Tabs, TabsContent, TabsTrigger } from '@/shared/components/ui/tabs';
import { ScrollTabsList } from '@/shared/components/ui/scroll-tabs-list';
import { BoardView } from './BoardView';
import { CreateTaskDialog } from './CreateTaskDialog';
import { ByPersonTab } from './dashboard/ByPersonTab';
import { ContributionTab } from './dashboard/ContributionTab';
import { HistoryTab } from './dashboard/HistoryTab';
import { resolveTab, TAB_LABELS, tabsForScope, type TabId } from './dashboard/logic';
import { OverviewTab } from './dashboard/OverviewTab';
import { ScrumTab } from './dashboard/ScrumTab';
import { useDashboard } from './dashboard/useDashboard';
import { useTabParams } from './dashboard/useTabParams';
import { useShortcut } from '@/shared/shortcuts/ShortcutsProvider';
import { shortcutHint } from '@/shared/shortcuts/catalogue';

/**
 * /tasks. Which tabs somebody gets is decided by the scope the server returns
 * with the dashboard, never by looking at their roles here.
 */
export function TasksPage() {
  const params = useTabParams();
  const { data, isPending } = useDashboard(params.team);
  const [createOpen, setCreateOpen] = useState(false);
  useShortcut('task.new', () => setCreateOpen(true));

  const scope = data?.scope;
  const tab = resolveTab(params.tab, scope);
  // If the dashboard failed there is no scope to go on. The board does not
  // depend on it, and the tab that was asked for shows its own error.
  const tabs: TabId[] = scope ? tabsForScope(scope) : [...new Set<TabId>(['overview', tab, 'board'])];

  return (
    <Tabs value={tab} onValueChange={params.selectTab} className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2 md:gap-3 md:px-6">
        {/* The heading goes on a phone: the navigation already says where you
            are, and the row is worth more as tabs than as a label. */}
        <h1 className="hidden text-xl font-bold md:block">Tasks</h1>
        {isPending ? (
          <div className="h-10 min-w-0 flex-1 animate-pulse rounded-md bg-muted md:max-w-md" aria-hidden />
        ) : (
          <ScrollTabsList value={tab} aria-label="Task views" wrapperClassName="flex-1 md:flex-none">
            {tabs.map((id) => (
              <TabsTrigger key={id} value={id}>
                {TAB_LABELS[id]}
              </TabsTrigger>
            ))}
          </ScrollTabsList>
        )}
        {/* Without the sidebar, the way to the projects is from here. */}
        <Button asChild variant="outline" size="icon" className="ml-auto shrink-0 lg:hidden" aria-label="Projects">
          <Link to="/projects">
            <FolderGit2 className="h-5 w-5" aria-hidden="true" />
          </Link>
        </Button>
        <Button className="hidden md:ml-0 md:inline-flex lg:ml-auto" size="sm" onClick={() => setCreateOpen(true)} {...shortcutHint('task.new', 'New task')}>
          New Task
        </Button>
        <Button size="icon" className="shrink-0 md:hidden" aria-label="New task" onClick={() => setCreateOpen(true)}>
          <Plus className="h-5 w-5" />
        </Button>
      </div>

      <TabsContent value={tab} className="mt-0 min-h-0 flex-1 focus-visible:ring-0 focus-visible:ring-offset-0">
        {tab === 'overview' && <OverviewTab />}
        {tab === 'person' && <ByPersonTab />}
        {tab === 'scrum' && <ScrumTab />}
        {tab === 'history' && <HistoryTab />}
        {tab === 'contribution' && <ContributionTab />}
        {tab === 'board' && <BoardView managesSomebody={scope === 'team' || scope === 'org'} />}
      </TabsContent>

      <CreateTaskDialog open={createOpen} onOpenChange={setCreateOpen} />
    </Tabs>
  );
}
