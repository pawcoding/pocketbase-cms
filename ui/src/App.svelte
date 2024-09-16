<script>
    import "./scss/main.scss";

    import Router, { replace, link } from "svelte-spa-router";
    import active from "svelte-spa-router/active";
    import routes from "./routes";
    import ApiClient from "@/utils/ApiClient";
    import CommonHelper from "@/utils/CommonHelper";
    import tooltip from "@/actions/tooltip";
    import Toasts from "@/components/base/Toasts.svelte";
    import Toggler from "@/components/base/Toggler.svelte";
    import Confirmation from "@/components/base/Confirmation.svelte";
    import { pageTitle, appName, hideControls } from "@/stores/app";
    import { admin } from "@/stores/admin";
    import { setErrors } from "@/stores/errors";
    import { resetConfirmation } from "@/stores/confirmation";
    import TinyMCE from "@/components/base/TinyMCE.svelte";
    import AdminUpsertPanel from "@/components/admins/AdminUpsertPanel.svelte";

    let oldLocation = undefined;

    let adminUpsertPanel;
    let showAppSidebar = false;

    let isTinyMCEPreloaded = false;

    $: if ($admin?.id) {
        loadSettings();
    }

    function handleRouteLoading(e) {
        if (e?.detail?.location === oldLocation) {
            return; // not an actual change
        }

        showAppSidebar = !!e?.detail?.userData?.showAppSidebar;

        oldLocation = e?.detail?.location;

        // resets
        $pageTitle = "";
        setErrors({});
        resetConfirmation();
    }

    function handleRouteFailure() {
        replace("/");
    }

    async function loadSettings() {
        if (!$admin?.id) {
            return;
        }

        try {
            const settings = await ApiClient.settings.getAll({
                $cancelKey: "initialAppSettings",
            });
            $appName = settings?.meta?.appName || "";
            $hideControls = !!settings?.meta?.hideControls || !$admin.superAdmin;
        } catch (err) {
            if (!err?.isAbort) {
                console.warn("Failed to load app settings.", err);
            }
        }
    }

    function logout() {
        ApiClient.logout();
    }
</script>

<svelte:head>
    <title>{CommonHelper.joinNonEmpty([$pageTitle, $appName, "PocketBase CMS"], " - ")}</title>
</svelte:head>

<div class="app-layout">
    {#if $admin?.id && showAppSidebar}
        <aside class="app-sidebar">
            <a href="/" class="logo logo-sm" use:link>
                <img
                    src="{import.meta.env.BASE_URL}images/logo.svg"
                    alt="PocketBase logo"
                    width="40"
                    height="40"
                />

                <strong>
                    CMS
                </strong>
            </a>

            <nav class="main-menu">
                <a
                    href="/collections"
                    class="menu-item"
                    aria-label="Collections"
                    use:link
                    use:active={{ path: "/collections/?.*", className: "current-route" }}
                    use:tooltip={{ text: "Collections", position: "right" }}
                >
                    <i class="ri-database-2-line" />
                </a>

                {#if $admin.superAdmin}
                    <a
                        href="/logs"
                        class="menu-item"
                        aria-label="Logs"
                        use:link
                        use:active={{ path: "/logs/?.*", className: "current-route" }}
                        use:tooltip={{ text: "Logs", position: "right" }}
                    >
                        <i class="ri-line-chart-line" />
                    </a>
                    <a
                        href="/settings"
                        class="menu-item"
                        aria-label="Settings"
                        use:link
                        use:active={{ path: "/settings/?.*", className: "current-route" }}
                        use:tooltip={{ text: "Settings", position: "right" }}
                    >
                        <i class="ri-tools-line" />
                    </a>
                {/if}
            </nav>

            <div
                tabindex="0"
                role="button"
                aria-label="Logged admin menu"
                class="thumb thumb-circle link-hint closable"
            >
                <img
                    src="{import.meta.env.BASE_URL}images/avatars/avatar{$admin?.avatar || 0}.svg"
                    alt="Avatar"
                    aria-hidden="true"
                />
                <Toggler class="dropdown dropdown-nowrap dropdown-upside dropdown-left">
                    <button
                        type="button"
                        class="dropdown-item closable"
                        role="menuitem"
                        on:click={() => adminUpsertPanel?.show($admin)}
                    >
                        <i class="ri-user-line" aria-hidden="true" />
                        <span class="txt">Profile</span>
                    </button>

                    <hr />
                    <button type="button" class="dropdown-item closable" role="menuitem" on:click={logout}>
                        <i class="ri-logout-circle-line" aria-hidden="true" />
                        <span class="txt">Logout</span>
                    </button>
                </Toggler>
            </div>
        </aside>
    {/if}

    <div class="app-body">
        <Router {routes} on:routeLoading={handleRouteLoading} on:conditionsFailed={handleRouteFailure} />

        <Toasts />
    </div>
</div>

<Confirmation />

<AdminUpsertPanel bind:this={adminUpsertPanel} />

{#if showAppSidebar && !isTinyMCEPreloaded}
    <div class="tinymce-preloader hidden">
        <TinyMCE
            conf={CommonHelper.defaultEditorOptions()}
            on:init={() => {
                isTinyMCEPreloaded = true;
            }}
        />
    </div>
{/if}
