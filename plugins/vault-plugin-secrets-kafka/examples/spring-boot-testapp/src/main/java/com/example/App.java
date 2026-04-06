package com.example;

import org.apache.kafka.clients.admin.AdminClientConfig;
import org.apache.kafka.clients.consumer.ConsumerConfig;
import org.apache.kafka.clients.consumer.ConsumerRecord;
import org.apache.kafka.clients.consumer.ConsumerRecords;
import org.apache.kafka.clients.consumer.KafkaConsumer;
import org.apache.kafka.clients.producer.ProducerConfig;
import org.apache.kafka.clients.producer.RecordMetadata;
import org.apache.kafka.common.serialization.StringDeserializer;
import org.apache.kafka.common.serialization.StringSerializer;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.kafka.core.DefaultKafkaProducerFactory;
import org.springframework.kafka.core.KafkaTemplate;
import org.springframework.vault.core.VaultOperations;
import org.springframework.vault.support.VaultResponse;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;

import java.time.Duration;
import java.time.Instant;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Properties;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.io.PrintWriter;
import java.io.StringWriter;

@SpringBootApplication
public class App {

    public static void main(String[] args) {
        SpringApplication.run(App.class, args);
    }

    private static String env(String k, String def) {
        String v = System.getenv(k);
        return v == null || v.isBlank() ? def : v;
    }

    private static String req(Map<String, Object> m, String k) {
        Object v = m.get(k);
        if (v == null) throw new IllegalStateException("Missing required field '" + k + "' in Vault response");
        String s = String.valueOf(v);
        if (s.isBlank()) throw new IllegalStateException("Missing required field '" + k + "' in Vault response");
        return s;
    }

    private static String opt(Map<String, Object> m, String k, String def) {
        Object v = m.get(k);
        if (v == null) return def;
        String s = String.valueOf(v);
        return s.isBlank() ? def : s;
    }

    @RestController
    class ApiController {
        private final VaultOperations vault;
        private final Object vaultLock = new Object();
        private final ScheduledExecutorService scheduler = Executors.newSingleThreadScheduledExecutor();

        private volatile CachedLease cached = null;
        private volatile PollStatus lastPoll = PollStatus.idle();
        private volatile Map<String, Object> lastKafkaRaw = Map.of("status", "IDLE");

        private final Object historyLock = new Object();
        private final ArrayDeque<HistoryPoint> history = new ArrayDeque<>(60);
        private volatile boolean pollingEnabled = false;
        private volatile String pollingRole = env("VAULT_KAFKA_ROLE", "ci");
        private volatile String pollingKind = "dynamic"; // dynamic | static

        ApiController(VaultOperations vault) {
            this.vault = vault;
            scheduler.scheduleAtFixedRate(this::pollTick, 0, 1, TimeUnit.SECONDS);
        }

        @GetMapping("/api/config")
        public Map<String, Object> config() {
            return Map.of(
                    "vaultAddr", env("VAULT_ADDR", "http://vault:8200"),
                    "role", env("VAULT_KAFKA_ROLE", "ci"),
                    "topic", env("TEST_TOPIC", "test-topic")
            );
        }

        @GetMapping("/api/roles")
        public Map<String, Object> roles() {
            return rolesByKind("dynamic");
        }

        @GetMapping("/api/static-roles")
        public Map<String, Object> staticRoles() {
            return rolesByKind("static");
        }

        private Map<String, Object> rolesByKind(String kind) {
            String p = "dynamic".equals(kind) ? "kafka/roles" : "kafka/static-roles";
            List<String> keys;
            synchronized (vaultLock) {
                keys = vault.list(p);
            }
            if (keys == null) keys = List.of();
            // Normalize list output (some Vault list responses include trailing slashes).
            List<String> roles = keys.stream().map(k -> k.endsWith("/") ? k.substring(0, k.length() - 1) : k).toList();
            return Map.of("roles", roles, "kind", kind);
        }

        @GetMapping("/api/status")
        public PollStatus status() {
            return lastPoll;
        }

        @GetMapping("/api/issued")
        public Map<String, Object> issued() {
            CachedLease c = cached;
            if (c == null) return Map.of("ok", false, "error", "No issued credentials yet");
            Map<String, Object> normalized = normalize(c.creds());
            return Map.of(
                    "ok", true,
                    "leaseId", c.creds().leaseId(),
                    "username", c.creds().username(),
                    "bootstrap", c.creds().bootstrapServers(),
                    "renewable", c.renewable(),
                    "leaseDurationSec", c.leaseDurationSec(),
                    "issuedAt", c.issuedAt(),
                    "expiresAt", c.expiresAt(),
                    "normalized", normalized,
                    "raw", c.creds().raw()
            );
        }

        @GetMapping("/api/role/{role}")
        public Map<String, Object> role(@PathVariable("role") String role) {
            VaultResponse r;
            synchronized (vaultLock) {
                r = vault.read("kafka/roles/" + role);
            }
            if (r == null) return Map.of("ok", false, "error", "role not found");
            return Map.of("ok", true, "data", Objects.requireNonNullElse(r.getData(), Map.of()));
        }

        @GetMapping("/api/static-role/{role}")
        public Map<String, Object> staticRole(@PathVariable("role") String role) {
            VaultResponse r;
            synchronized (vaultLock) {
                r = vault.read("kafka/static-roles/" + role);
            }
            if (r == null) return Map.of("ok", false, "error", "role not found");
            return Map.of("ok", true, "data", Objects.requireNonNullElse(r.getData(), Map.of()));
        }

        @GetMapping("/api/kafka/raw")
        public Map<String, Object> kafkaRaw() {
            return lastKafkaRaw;
        }

        @GetMapping("/api/history")
        public Map<String, Object> history() {
            synchronized (historyLock) {
                return Map.of(
                        "pollingEnabled", pollingEnabled,
                        "role", pollingRole,
                        "points", new ArrayList<>(history)
                );
            }
        }

        @PostMapping("/api/polling/start")
        public Map<String, Object> pollingStart(@RequestBody(required = false) Map<String, Object> body) {
            String role = body != null ? String.valueOf(body.getOrDefault("role", env("VAULT_KAFKA_ROLE", "ci"))) : env("VAULT_KAFKA_ROLE", "ci");
            String kind = body != null ? String.valueOf(body.getOrDefault("kind", "dynamic")) : "dynamic";
            this.pollingRole = role;
            this.pollingKind = kind;
            this.pollingEnabled = true;
            return Map.of("ok", true, "pollingEnabled", true, "role", pollingRole, "kind", pollingKind);
        }

        @PostMapping("/api/polling/stop")
        public Map<String, Object> pollingStop() {
            this.pollingEnabled = false;
            return Map.of("ok", true, "pollingEnabled", false);
        }

        @PostMapping("/api/issue/{role}")
        public Map<String, Object> issue(@PathVariable("role") String role) {
            CachedLease issued = issueNewLease("dynamic", role);
            this.cached = issued;
            return Map.of(
                    "leaseId", issued.creds().leaseId(),
                    "role", role,
                    "kind", "dynamic",
                    "username", issued.creds().username(),
                    "bootstrap", issued.creds().bootstrapServers(),
                    "raw", issued.creds().raw()
            );
        }

        @PostMapping("/api/static-issue/{role}")
        public Map<String, Object> staticIssue(@PathVariable("role") String role) {
            CachedLease issued = issueNewLease("static", role);
            this.cached = issued;
            return Map.of(
                    "leaseId", issued.creds().leaseId(),
                    "role", role,
                    "kind", "static",
                    "username", issued.creds().username(),
                    "bootstrap", issued.creds().bootstrapServers(),
                    "raw", issued.creds().raw()
            );
        }

        @PostMapping("/api/revoke")
        public Map<String, Object> revoke() {
            CachedLease c = this.cached;
            if (c == null || c.creds().leaseId().isBlank()) {
                return Map.of("ok", false, "error", "No issued credentials/lease_id");
            }
            synchronized (vaultLock) {
                vault.write("sys/leases/revoke", Map.of("lease_id", c.creds().leaseId()));
            }
            this.cached = null;
            return Map.of("ok", true, "leaseId", c.creds().leaseId());
        }

        @PostMapping("/api/renew")
        public Map<String, Object> renew() {
            CachedLease c = this.cached;
            if (c == null) return Map.of("ok", false, "error", "No cached lease");
            if (c.renewable()) {
                CachedLease renewed = renewLease(c, "MANUAL");
                this.cached = renewed;
                return Map.of(
                        "ok", true,
                        "action", "RENEW",
                        "leaseId", renewed.creds().leaseId(),
                        "leaseDurationSec", renewed.leaseDurationSec(),
                        "expiresAt", renewed.expiresAt()
                );
            }

            // If the lease isn't renewable, "renew" behaves as "reissue".
            CachedLease reissued = issueNewLease(pollingKind, pollingRole);
            this.cached = reissued;
            return Map.of(
                    "ok", true,
                    "action", "REISSUE",
                    "leaseId", reissued.creds().leaseId(),
                    "leaseDurationSec", reissued.leaseDurationSec(),
                    "expiresAt", reissued.expiresAt()
            );
        }

        private void pollTick() {
            if (!pollingEnabled) return;
            try {
                // 1-second validation loop:
                // - Reuse creds until TTL expiry
                // - Auto-renew when needed
                // - Refresh Kafka raw request result
                long started = System.nanoTime();
                LeaseDecision decision = ensureLease(pollingKind, pollingRole);
                CachedLease c = decision.lease();
                this.cached = c;

                Map<String, Object> kafka = kafkaProduceRaw(c.creds());
                this.lastKafkaRaw = kafka;

                long latencyMs = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started);
                addHistory(true, latencyMs, null, pollingRole);
                this.lastPoll = PollStatus.ok(Instant.now().toString(), pollingRole, decision.action(), latencyMs, leaseUi(c), kafka);
            } catch (Exception e) {
                addHistory(false, 0, classifyError(e), pollingRole);
                String err = stackTrace(e);
                this.lastKafkaRaw = Map.of("ok", false, "error", String.valueOf(e), "stack", err);
                this.lastPoll = PollStatus.fail(Instant.now().toString(), pollingRole, classifyError(e), err);
            }
        }

        private void addHistory(boolean ok, long latencyMs, String errorKind, String role) {
            synchronized (historyLock) {
                if (history.size() == 60) history.removeFirst();
                history.addLast(new HistoryPoint(Instant.now().toString(), ok, latencyMs, errorKind, role));
            }
        }

        private String classifyError(Exception e) {
            Throwable t = e;
            while (t.getCause() != null) t = t.getCause();
            String msg = String.valueOf(t.getMessage());
            if (msg.contains("SaslAuthenticationException") || msg.contains("SASL") || msg.contains("auth")) return "AUTH";
            if (msg.contains("Timeout") || msg.contains("timed out")) return "TIMEOUT";
            if (msg.contains("UnknownHost") || msg.contains("Unresolved") || msg.contains("No resolvable bootstrap")) return "DNS";
            return t.getClass().getSimpleName();
        }

        private CachedLease issueNewLease(String kind, String role) {
            String path = "static".equals(kind) ? ("kafka/static-creds/" + role) : ("kafka/creds/" + role);
            VaultResponse resp;
            synchronized (vaultLock) {
                resp = vault.read(path);
            }
            if (resp == null) throw new IllegalStateException("Vault read returned null for " + path);
            Map<String, Object> data = Objects.requireNonNullElse(resp.getData(), Map.of());

            String leaseId = String.valueOf(resp.getLeaseId());
            long leaseDurationSec = resp.getLeaseDuration();
            boolean renewable = resp.isRenewable();
            Instant now = Instant.now();
            Instant expiresAt = now.plusSeconds(Math.max(1, leaseDurationSec));
            String bootstrap = req(data, "bootstrap_servers");
            String securityProtocol = opt(data, "security_protocol", "SASL_PLAINTEXT");
            String saslMechanism = opt(data, "sasl_mechanism", "SCRAM-SHA-256");
            String username = req(data, "username");
            String password = req(data, "password");

            Map<String, Object> raw = new ConcurrentHashMap<>();
            raw.put("lease_id", leaseId);
            raw.put("data", new HashMap<>(data));
            raw.put("lease_duration", leaseDurationSec);
            raw.put("renewable", renewable);
            raw.put("kind", kind);
            raw.put("path", path);
            raw.put("role", role);

            IssuedCreds creds = new IssuedCreds(leaseId, bootstrap, securityProtocol, saslMechanism, username, password, raw);
            return new CachedLease(creds, now.toString(), leaseDurationSec, renewable, expiresAt.toString(), null);
        }

        private Map<String, Object> producerProps(IssuedCreds c) {
            String mech = String.valueOf(c.saslMechanism());
            String jaasModule = mech.equalsIgnoreCase("PLAIN")
                    ? "org.apache.kafka.common.security.plain.PlainLoginModule"
                    : "org.apache.kafka.common.security.scram.ScramLoginModule";
            String jaas = jaasModule + " required "
                    + "username=\"" + c.username() + "\" "
                    + "password=\"" + c.password() + "\";";

            Map<String, Object> props = new HashMap<>();
            props.put(ProducerConfig.BOOTSTRAP_SERVERS_CONFIG, c.bootstrapServers());
            props.put(ProducerConfig.KEY_SERIALIZER_CLASS_CONFIG, StringSerializer.class);
            props.put(ProducerConfig.VALUE_SERIALIZER_CLASS_CONFIG, StringSerializer.class);
            props.put("security.protocol", "n/a".equalsIgnoreCase(c.securityProtocol()) ? "SASL_PLAINTEXT" : c.securityProtocol());
            props.put("sasl.mechanism", "n/a".equalsIgnoreCase(c.saslMechanism()) ? "SCRAM-SHA-256" : c.saslMechanism());
            props.put("sasl.jaas.config", jaas);
            return props;
        }

        private Map<String, Object> normalize(IssuedCreds c) {
            Map<String, Object> m = new HashMap<>();
            m.put("bootstrap_servers", c.bootstrapServers());
            m.put("security_protocol", c.securityProtocol());
            m.put("sasl_mechanism", c.saslMechanism());
            m.put("username", c.username());
            m.put("password", c.password().isBlank() ? "" : "****");
            m.put("kafka_client", Map.of(
                    "bootstrap.servers", c.bootstrapServers(),
                    "security.protocol", c.securityProtocol(),
                    "sasl.mechanism", c.saslMechanism(),
                    "sasl.jaas.config", "org.apache.kafka.common.security.scram.ScramLoginModule required username=\"" + c.username() + "\" password=\"****\";"
            ));
            return m;
        }

        private Map<String, Object> kafkaProduceRaw(IssuedCreds c) throws Exception {
            String topic = env("TEST_TOPIC", "test-topic");
            Map<String, Object> props = producerProps(c);
            KafkaTemplate<String, String> template = new KafkaTemplate<>(new DefaultKafkaProducerFactory<>(props));
            String key = "poll";
            String value = "p-" + UUID.randomUUID();
            try {
                var sendResult = template.send(topic, key, value)
                        .get(5, TimeUnit.SECONDS)
                        ;
                RecordMetadata md = sendResult != null ? sendResult.getRecordMetadata() : null;
                if (md == null) {
                    return Map.of(
                            "ok", true,
                            "request", Map.of("topic", topic, "key", key, "value", value),
                            "result", Map.of("note", "sendResult/recordMetadata is null")
                    );
                }
                return Map.of(
                        "ok", true,
                        "request", Map.of("topic", topic, "key", key, "value", value),
                        "result", Map.of(
                                "topic", md.topic(),
                                "partition", md.partition(),
                                "offset", md.offset(),
                                "timestamp", md.timestamp()
                        )
                );
            } finally {
                template.destroy();
            }
        }

        private String stackTrace(Throwable t) {
            StringWriter sw = new StringWriter();
            PrintWriter pw = new PrintWriter(sw);
            t.printStackTrace(pw);
            pw.flush();
            return sw.toString();
        }

        private String validateRoundTrip(IssuedCreds c, String topic) throws Exception {
            Map<String, Object> props = producerProps(c);
            KafkaTemplate<String, String> template = new KafkaTemplate<>(new DefaultKafkaProducerFactory<>(props));
            String value = "v-" + UUID.randomUUID();
            try {
                template.send(topic, "k", value)
                        .get(10, TimeUnit.SECONDS);
            } finally {
                template.destroy();
            }

            Properties consumerProps = new Properties();
            consumerProps.put(ConsumerConfig.BOOTSTRAP_SERVERS_CONFIG, c.bootstrapServers());
            consumerProps.put(ConsumerConfig.KEY_DESERIALIZER_CLASS_CONFIG, StringDeserializer.class.getName());
            consumerProps.put(ConsumerConfig.VALUE_DESERIALIZER_CLASS_CONFIG, StringDeserializer.class.getName());
            consumerProps.put(ConsumerConfig.AUTO_OFFSET_RESET_CONFIG, "earliest");
            consumerProps.put(ConsumerConfig.ENABLE_AUTO_COMMIT_CONFIG, "true");
            consumerProps.put(ConsumerConfig.GROUP_ID_CONFIG, "vaulttest-" + c.username());
            consumerProps.put("security.protocol", c.securityProtocol());
            consumerProps.put("sasl.mechanism", c.saslMechanism());
            consumerProps.put("sasl.jaas.config", "org.apache.kafka.common.security.scram.ScramLoginModule required "
                    + "username=\"" + c.username() + "\" "
                    + "password=\"" + c.password() + "\";");

            try (KafkaConsumer<String, String> consumer = new KafkaConsumer<>(consumerProps)) {
                consumer.subscribe(List.of(topic));
                boolean ok = false;
                long deadline = System.currentTimeMillis() + 15000;
                while (System.currentTimeMillis() < deadline && !ok) {
                    ConsumerRecords<String, String> records = consumer.poll(Duration.ofMillis(500));
                    for (ConsumerRecord<String, String> r : records) {
                        if (value.equals(r.value())) {
                            ok = true;
                            break;
                        }
                    }
                }
                if (!ok) throw new IllegalStateException("Did not consume produced message within timeout");
            }
            return value;
        }

        private Map<String, Object> leaseUi(CachedLease c) {
            return Map.of(
                    "leaseId", c.creds().leaseId(),
                    "username", c.creds().username(),
                    "renewable", c.renewable(),
                    "leaseDurationSec", c.leaseDurationSec(),
                    "issuedAt", c.issuedAt(),
                    "expiresAt", c.expiresAt(),
                    "lastRenewAt", Objects.toString(c.lastRenewAt(), "")
            );
        }

        private LeaseDecision ensureLease(String kind, String role) {
            CachedLease c = this.cached;
            Instant now = Instant.now();
            if (c == null) {
                CachedLease n = issueNewLease(kind, role);
                return new LeaseDecision(n, "ISSUE");
            }

            // If kind/role changed, do not reuse cached creds; reissue immediately.
            String cachedKind = String.valueOf(c.creds().raw().getOrDefault("kind", ""));
            String cachedRole = String.valueOf(c.creds().raw().getOrDefault("role", ""));
            if (!Objects.equals(cachedKind, kind) || !Objects.equals(cachedRole, role)) {
                CachedLease n = issueNewLease(kind, role);
                return new LeaseDecision(n, "REISSUE_ROLE_CHANGED");
            }

            Instant expiresAt = Instant.parse(c.expiresAt());
            long remainingSec = Duration.between(now, expiresAt).getSeconds();
            if (remainingSec <= 0) {
                CachedLease n = issueNewLease(kind, role);
                return new LeaseDecision(n, "REISSUE_EXPIRED");
            }

            // Auto-renew when within 30s to expiry (if renewable).
            if (c.renewable() && remainingSec <= 30) {
                CachedLease r = renewLease(c, "AUTO");
                return new LeaseDecision(r, "RENEW");
            }
            return new LeaseDecision(c, "REUSE");
        }

        private CachedLease renewLease(CachedLease c, String reason) {
            VaultResponse r;
            synchronized (vaultLock) {
                r = vault.write("sys/leases/renew", Map.of(
                        "lease_id", c.creds().leaseId(),
                        "increment", c.leaseDurationSec()
                ));
            }
            if (r == null) throw new IllegalStateException("Vault renew returned null");
            long newDur = r.getLeaseDuration() > 0 ? r.getLeaseDuration() : c.leaseDurationSec();
            boolean renewable = r.isRenewable();
            Instant now = Instant.now();
            Instant expiresAt = now.plusSeconds(Math.max(1, newDur));

            Map<String, Object> raw = new ConcurrentHashMap<>(c.creds().raw());
            raw.put("renew_reason", reason);
            raw.put("renew_lease_duration", newDur);
            raw.put("renewable", renewable);

            IssuedCreds creds = new IssuedCreds(
                    c.creds().leaseId(),
                    c.creds().bootstrapServers(),
                    c.creds().securityProtocol(),
                    c.creds().saslMechanism(),
                    c.creds().username(),
                    c.creds().password(),
                    raw
            );

            return new CachedLease(creds, c.issuedAt(), newDur, renewable, expiresAt.toString(), now.toString());
        }
    }

    record PollStatus(
            String ts,
            String status,
            String role,
            String action,
            long latencyMs,
            String errorKind,
            String error,
            Map<String, Object> lease,
            Map<String, Object> kafka
    ) {
        static PollStatus idle() {
            return new PollStatus(Instant.now().toString(), "IDLE", null, null, 0, null, null, null, null);
        }

        static PollStatus ok(String ts, String role, String action, long latencyMs, Map<String, Object> lease, Map<String, Object> kafka) {
            return new PollStatus(ts, "SUCCESS", role, action, latencyMs, null, null, lease, kafka);
        }

        static PollStatus fail(String ts, String role, String errorKind, String error) {
            return new PollStatus(ts, "FAILED", role, null, 0, errorKind, error, null, null);
        }
    }

    record IssuedCreds(
            String leaseId,
            String bootstrapServers,
            String securityProtocol,
            String saslMechanism,
            String username,
            String password,
            Map<String, Object> raw
    ) {
    }

    record CachedLease(
            IssuedCreds creds,
            String issuedAt,
            long leaseDurationSec,
            boolean renewable,
            String expiresAt,
            String lastRenewAt
    ) {
    }

    record LeaseDecision(CachedLease lease, String action) {
    }

    record HistoryPoint(String ts, boolean ok, long latencyMs, String errorKind, String role) {
    }
}

