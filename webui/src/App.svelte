<script lang="ts">
  import Router from "svelte-spa-router";
  import Header from "./components/Header.svelte";
  import Dashboard from "./pages/Dashboard.svelte";
  import Runs from "./pages/Runs.svelte";
  import RunDetail from "./pages/RunDetail.svelte";
  import PlanView from "./pages/PlanView.svelte";
  import Units from "./pages/Units.svelte";

  const routes = {
    "/": Dashboard,
    "/runs": Runs,
    "/runs/:id": RunDetail,
    "/plan": PlanView,
    "/units": Units,
  };
</script>

<div class="min-h-screen bg-base-100 text-base-content font-sans">
  <Header />
  <main>
    <Router {routes} />
  </main>
</div>
