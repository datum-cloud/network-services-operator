import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { Title } from '@datum-cloud/datum-ui/typography';
import { ChartColumnIcon } from 'lucide-react';

const SILHOUETTE = [
  18, 26, 22, 30, 38, 34, 50, 44, 36, 56, 64, 60, 72, 68, 52, 48, 58, 74, 84, 78, 62, 56, 46, 60,
  70, 76, 66, 50, 42, 38, 54, 64,
];

const STATS = ['active', 'new/s', 'throughput'];

export function NlbMetricsPlaceholder() {
  return (
    <Card
      size="sm"
      className="w-full min-w-0"
      data-testid="networking-plugin-nlb-metrics-placeholder">
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center gap-2">
          <Title as="h2" level={5}>
            Connections
          </Title>
          <span className="flex-1" />
          {STATS.map((stat) => (
            <span key={stat} className="nlb-chip">
              <span className="text-muted-foreground">{stat}</span>—
            </span>
          ))}
        </div>
        <div className="relative" style={{ height: '10rem' }}>
          <div aria-hidden className="flex h-full items-end" style={{ gap: '0.25rem' }}>
            {SILHOUETTE.map((height, i) => (
              <span
                key={i}
                className="flex-1"
                style={{
                  height: `${height}%`,
                  background: 'var(--muted)',
                  borderRadius: '2px 2px 0 0',
                }}
              />
            ))}
          </div>
          <div className="absolute inset-0 flex items-center justify-center">
            <span className="nlb-chip text-muted-foreground">
              <Icon icon={ChartColumnIcon} size={13} />
              Connection metrics are coming soon
            </span>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
