use super::*;
use axum::{extract::State, http::StatusCode, response::IntoResponse, routing::get, Router};
use std::sync::atomic::AtomicUsize;
use tokio::sync::Barrier;

struct Backend {
    calls: AtomicUsize,
    status: AtomicU32,
    delay: Duration,
    body: String,
}

async fn lookup(
    State(backend): State<Arc<Backend>>,
    headers: axum::http::HeaderMap,
) -> impl IntoResponse {
    assert_eq!(
        headers[axum::http::header::AUTHORIZATION],
        "Bearer fixture-token"
    );
    backend.calls.fetch_add(1, Ordering::Relaxed);
    tokio::time::sleep(backend.delay).await;
    (
        StatusCode::from_u16(backend.status.load(Ordering::Relaxed) as u16).unwrap(),
        backend.body.clone(),
    )
}

struct Fixture {
    resolver: Arc<LinkResolver>,
    backend: Arc<Backend>,
    server: tokio::task::JoinHandle<()>,
}

impl Fixture {
    async fn new(status: u16, body: &str, delay: Duration) -> Self {
        let _ = rustls::crypto::ring::default_provider().install_default();
        let backend = Arc::new(Backend {
            calls: AtomicUsize::new(0),
            status: AtomicU32::new(status.into()),
            delay,
            body: body.into(),
        });
        let app = Router::new()
            .route("/api/v1/internal/tracked-links/:id", get(lookup))
            .with_state(backend.clone());
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let resolver = Arc::new(LinkResolver::new(
            format!("http://{}", listener.local_addr().unwrap()),
            "fixture-token".into(),
        ));
        let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
        Self {
            resolver,
            backend,
            server,
        }
    }

    async fn wave(&self, ticket: &str, callers: usize, same_source: bool) -> Vec<Resolution> {
        let gate = Arc::new(Barrier::new(callers));
        let mut tasks = Vec::new();
        for i in 0..callers {
            let (gate, resolver) = (gate.clone(), self.resolver.clone());
            let ticket = ticket.to_string();
            tasks.push(tokio::spawn(async move {
                gate.wait().await;
                resolver
                    .resolve(
                        &ticket,
                        &format!("source-{}", if same_source { 0 } else { i }),
                    )
                    .await
            }));
        }
        let mut results = Vec::new();
        for task in tasks {
            results.push(task.await.unwrap());
        }
        results
    }

    fn calls(&self) -> usize {
        self.backend.calls.load(Ordering::Relaxed)
    }
}

impl Drop for Fixture {
    fn drop(&mut self) {
        self.server.abort();
    }
}

const LINK: &str = r#"{"destination":"https://example.com/a?q=1#top","task_id":"11111111-2222-3333-4444-555555555555","identify":true}"#;

fn assert_link(result: Resolution, identify: bool) {
    match result {
        Resolution::Found(link) => {
            assert_eq!(link.destination, "https://example.com/a?q=1#top");
            assert_eq!(link.task_id, "11111111-2222-3333-4444-555555555555");
            assert_eq!(link.identify, identify);
        }
        _ => panic!("expected a verified destination"),
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn concurrent_ticket_lookups_share_one_backend_request() {
    let fixture = Fixture::new(200, LINK, Duration::from_millis(40)).await;
    for result in fixture.wave("ticket", 32, false).await {
        assert_link(result, true);
    }
    assert_eq!(fixture.calls(), 1);
    assert_link(fixture.resolver.resolve("ticket", "source-0").await, true);
    assert_eq!(
        fixture.calls(),
        1,
        "repeat clicks use the same positive cache"
    );
    assert_link(
        fixture.resolver.resolve("another-ticket", "source-0").await,
        true,
    );
    assert_eq!(fixture.calls(), 2, "different tickets are not merged");
}

#[tokio::test]
async fn old_ticket_payloads_still_default_to_no_identification() {
    let fixture = Fixture::new(200, &LINK.replace(",\"identify\":true", ""), Duration::ZERO).await;
    assert_link(fixture.resolver.resolve("ticket", "source").await, false);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn coalesced_unknown_tickets_charge_each_source_and_keep_the_miss_budget() {
    let fixture = Fixture::new(404, "unknown", Duration::from_millis(40)).await;
    for result in fixture.wave("unknown", 8, false).await {
        assert!(matches!(result, Resolution::NotFound));
    }
    assert_eq!(fixture.calls(), 1);
    for i in 0..8 {
        let source = format!("source-{i}");
        assert_eq!(
            fixture
                .resolver
                .miss_budget
                .get(&source)
                .await
                .unwrap()
                .load(Ordering::Relaxed),
            1
        );
    }
    for result in fixture.wave("unknown", 12, true).await {
        assert!(matches!(result, Resolution::NotFound));
    }
    assert_eq!(fixture.calls(), 1, "negative hits never look up again");
    assert_eq!(
        fixture
            .resolver
            .miss_budget
            .get("source-0")
            .await
            .unwrap()
            .load(Ordering::Relaxed),
        13
    );
    assert!(matches!(
        fixture.resolver.resolve("fresh", "source-0").await,
        Resolution::NotFound
    ));
    assert_eq!(
        fixture.calls(),
        1,
        "miss budget must still stop backend requests"
    );
    fixture.backend.status.store(200, Ordering::Relaxed);
    // A confirmed positive ticket remains available even to a source that spent its miss budget.
    fixture
        .resolver
        .found
        .insert(
            "positive".into(),
            ResolvedLink {
                destination: "https://example.com/a?q=1#top".into(),
                task_id: "11111111-2222-3333-4444-555555555555".into(),
                identify: true,
            },
        )
        .await;
    assert_link(fixture.resolver.resolve("positive", "source-0").await, true);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn backend_errors_are_not_cached_or_charged_as_unknown_tickets() {
    let fixture = Fixture::new(503, LINK, Duration::from_millis(40)).await;
    for result in fixture.wave("ticket", 8, false).await {
        assert!(matches!(result, Resolution::Unavailable));
    }
    assert_eq!(fixture.calls(), 1);
    assert_eq!(fixture.resolver.breaker_failures.load(Ordering::Relaxed), 1);
    assert!(!fixture.resolver.not_found.contains_key("ticket"));
    for i in 0..8 {
        assert!(fixture.resolver.miss_allowed(&format!("source-{i}")).await);
    }
    fixture.backend.status.store(200, Ordering::Relaxed);
    assert_link(fixture.resolver.resolve("ticket", "source-0").await, true);
    assert_eq!(fixture.calls(), 2, "a backend error does not prevent retry");
    assert_eq!(fixture.resolver.breaker_failures.load(Ordering::Relaxed), 0);
}

#[tokio::test]
async fn decode_errors_fail_closed_and_actual_failures_still_trip_the_breaker() {
    let fixture = Fixture::new(200, "not json", Duration::ZERO).await;
    for _ in 0..BREAKER_TRIP {
        assert!(matches!(
            fixture.resolver.resolve("ticket", "source").await,
            Resolution::Unavailable
        ));
    }
    assert_eq!(fixture.calls(), BREAKER_TRIP as usize);
    assert!(fixture.resolver.breaker_is_open());
    assert!(matches!(
        fixture.resolver.resolve("other-ticket", "source").await,
        Resolution::Unavailable
    ));
    assert_eq!(fixture.calls(), BREAKER_TRIP as usize);
    assert!(!fixture.resolver.not_found.contains_key("ticket"));
    assert!(fixture.resolver.miss_allowed("source").await);
}

#[tokio::test]
async fn denied_internal_requests_fail_closed_without_becoming_cache_entries() {
    for status in [401, 403] {
        let fixture = Fixture::new(status, LINK, Duration::ZERO).await;
        assert!(matches!(
            fixture.resolver.resolve("ticket", "source").await,
            Resolution::Unavailable
        ));
        assert!(!fixture.resolver.not_found.contains_key("ticket"));
        assert!(fixture.resolver.found.get("ticket").await.is_none());
        fixture.backend.status.store(200, Ordering::Relaxed);
        assert_link(fixture.resolver.resolve("ticket", "source").await, true);
        assert_eq!(fixture.calls(), 2);
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore = "synthetic localhost call-count benchmark; run with --ignored --nocapture"]
async fn benchmark_concurrent_ticket_resolution() {
    let fixture = Fixture::new(200, LINK, Duration::from_millis(40)).await;
    let started = Instant::now();
    const TICKETS: usize = 25;
    const CALLERS: usize = 32;
    for i in 0..TICKETS {
        for result in fixture.wave(&format!("ticket-{i}"), CALLERS, false).await {
            assert_link(result, true);
        }
    }
    println!("synthetic cold-cache tickets={TICKETS}, callers_per_ticket={CALLERS}, backend_calls={}, elapsed_ms={}", fixture.calls(), started.elapsed().as_millis());
}

// Stands in for the backend: link.acme.test belongs to another workspace.
async fn host_lookup(axum::extract::RawQuery(query): axum::extract::RawQuery) -> impl IntoResponse {
    if query.as_deref() == Some("host=link.acme.test") {
        (StatusCode::NOT_FOUND, String::new())
    } else {
        (StatusCode::OK, LINK.to_string())
    }
}

#[tokio::test]
async fn tickets_resolve_per_request_host() {
    let _ = rustls::crypto::ring::default_provider().install_default();
    let app = Router::new().route("/api/v1/internal/tracked-links/:id", get(host_lookup));
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let resolver = LinkResolver::new(
        format!("http://{}", listener.local_addr().unwrap()),
        "fixture-token".into(),
    );
    let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });

    assert_link(
        resolver
            .resolve_on("ticket", "s", "track.warmbly.test")
            .await,
        true,
    );
    assert!(
        matches!(
            resolver.resolve_on("ticket", "s", "link.acme.test").await,
            Resolution::NotFound
        ),
        "a positive answer on one host must not serve the ticket on another"
    );
    assert_link(resolver.resolve_on("ticket", "s", "").await, true);
    server.abort();
}
