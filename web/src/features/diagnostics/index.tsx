import { useState } from "react";
import { useResource } from "@/lib/hooks";
import { Details, Resource } from "@/components/data";
import { Button } from "@/components/ui/button";
import type { Row } from "@/lib/api";
export default function Diagnostics() {
  const [tick, setTick] = useState(0);
  const state = useResource<Row>("diagnostics", tick);
  return (
    <section className="panel inset">
      <div className="panel-heading">
        <h2>Service diagnostics</h2>
        <Button variant="outline" onClick={() => setTick((t) => t + 1)}>
          Refresh measurements
        </Button>
      </div>
      <p>
        DNS, list freshness, and statistics persistence are independent health
        signals.
      </p>
      <Resource state={state} retry={() => setTick((t) => t + 1)}>
        <Details value={state.data} />
      </Resource>
    </section>
  );
}
