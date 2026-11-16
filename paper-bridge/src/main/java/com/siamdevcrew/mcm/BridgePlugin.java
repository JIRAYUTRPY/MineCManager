package com.siamdevcrew.mcm;

import com.google.gson.Gson;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import net.kyori.adventure.text.Component;
import net.kyori.adventure.text.serializer.plain.PlainTextComponentSerializer;
import org.bukkit.Bukkit;
import org.bukkit.Material;
import org.bukkit.entity.Item;
import org.bukkit.entity.Player;
import org.bukkit.inventory.ItemStack;
import org.bukkit.inventory.meta.ItemMeta;
import org.bukkit.plugin.java.JavaPlugin;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.*;
import java.util.function.Supplier;

/** Bukkit access happens exclusively on the server thread. HTTP never executes commands. */
public final class BridgePlugin extends JavaPlugin {
    private final Gson gson = new Gson();
    private HttpServer server;
    private ThreadPoolExecutor workers;
    private byte[] authorization;

    @Override public void onEnable() {
        String token = System.getenv("MCM_PAPER_TOKEN");
        if (token == null || token.length() < 32) {
            getLogger().severe("MCM_PAPER_TOKEN of at least 32 characters is required; bridge disabled");
            getServer().getPluginManager().disablePlugin(this);
            return;
        }
        authorization = ("Bearer " + token).getBytes(StandardCharsets.UTF_8);
        try {
            server = HttpServer.create(new InetSocketAddress("127.0.0.1", 9082), 16);
            workers = new ThreadPoolExecutor(2, 4, 30, TimeUnit.SECONDS, new ArrayBlockingQueue<>(16), new ThreadPoolExecutor.AbortPolicy());
            server.setExecutor(workers);
            server.createContext("/v1/", this::handle);
            server.start();
        } catch (IOException e) {
            getLogger().severe("Cannot bind local bridge: " + e.getMessage());
            getServer().getPluginManager().disablePlugin(this);
        }
    }
    @Override public void onDisable() {
        if (server != null) server.stop(0);
        if (workers != null) workers.shutdownNow();
    }
    private void send(HttpExchange exchange, int status, Object value) throws IOException {
        byte[] bytes = gson.toJson(value).getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.getResponseHeaders().set("Cache-Control", "no-store");
        exchange.sendResponseHeaders(status, bytes.length);
        exchange.getResponseBody().write(bytes);
    }
    private void handle(HttpExchange exchange) throws IOException {
        try {
            String header = exchange.getRequestHeaders().getFirst("Authorization");
            if (header == null || !MessageDigest.isEqual(authorization, header.getBytes(StandardCharsets.UTF_8))) {
                send(exchange, 401, Map.of("error", "unauthorized")); return;
            }
            String path = exchange.getRequestURI().getPath();
            String method = exchange.getRequestMethod();
            if (method.equals("GET") && path.equals("/v1/players")) {
                send(exchange, 200, onMain(() -> Bukkit.getOnlinePlayers().stream().map(p -> Map.of("uuid", p.getUniqueId().toString(), "name", p.getName())).toList()));
            } else if (method.equals("GET") && path.equals("/v1/resources")) {
                send(exchange, 200, onMain(this::resources));
            } else {
                String[] parts = path.split("/");
                if (parts.length < 5 || !parts[1].equals("v1") || !parts[2].equals("players") || !parts[4].equals("inventory")) throw new Failure(404, "route not found");
                UUID uuid = UUID.fromString(parts[3]);
                if (method.equals("GET") && parts.length == 5) {
                    send(exchange, 200, onMain(() -> inventory(player(uuid))));
                } else if (method.equals("PUT") && parts.length == 6) {
                    int slot = Integer.parseInt(parts[5]);
                    if (slot < 0 || slot > 40) throw new Failure(400, "slot must be 0..40");
                    byte[] body = exchange.getRequestBody().readNBytes(16385);
                    if (body.length > 16384) throw new Failure(413, "request exceeds 16 KiB");
                    JsonObject input = JsonParser.parseString(new String(body, StandardCharsets.UTF_8)).getAsJsonObject();
                    RequestRules.item(input);
                    String expected = exchange.getRequestHeaders().getFirst("If-Match");
                    send(exchange, 200, onMain(() -> replace(player(uuid), slot, expected, input)));
                } else throw new Failure(405, "method not allowed");
            }
        } catch (Failure e) { send(exchange, e.status, Map.of("error", e.getMessage())); }
        catch (IllegalArgumentException | IllegalStateException | com.google.gson.JsonParseException e) { send(exchange, 400, Map.of("error", "invalid request")); }
        catch (Exception e) { getLogger().warning("Bridge request failed: " + e.getClass().getSimpleName()); send(exchange, 503, Map.of("error", "bridge unavailable; inspect state before retry")); }
        finally { exchange.close(); }
    }
    private <T> T onMain(Supplier<T> action) throws Exception {
        CompletableFuture<T> result = new CompletableFuture<>();
        var task = Bukkit.getScheduler().runTask(this, () -> {
            if (result.isCancelled()) return;
            try { result.complete(action.get()); } catch (Throwable e) { result.completeExceptionally(e); }
        });
        try { return result.get(3, TimeUnit.SECONDS); }
        catch (TimeoutException e) { result.cancel(false); task.cancel(); throw new Failure(503, "server tick timeout; inspect state before retry"); }
        catch (ExecutionException e) { if (e.getCause() instanceof Failure f) throw f; if(e.getCause() instanceof IllegalArgumentException a) throw a; throw e; }
    }
    private Player player(UUID id) { Player p=Bukkit.getPlayer(id); if(p==null)throw new Failure(404,"player must be online");return p; }
    private String hash(ItemStack item) {
        try { byte[] bytes = item == null || item.getType().isAir() ? new byte[0] : item.serializeAsBytes();return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes)); }
        catch (Exception e) { throw new IllegalStateException(e); }
    }
    private Map<String,Object> describe(ItemStack item,int slot) {
        Map<String,Object> out=new LinkedHashMap<>();out.put("slot",slot);out.put("sha256",hash(item));
        if(item==null||item.getType().isAir()){out.put("material","AIR");out.put("amount",0);return out;}
        out.put("material",item.getType().name());out.put("amount",item.getAmount());
        ItemMeta meta=item.getItemMeta();if(meta!=null&&meta.hasDisplayName())out.put("display_name",PlainTextComponentSerializer.plainText().serialize(meta.displayName()));
        return out;
    }
    private Map<String,Object> inventory(Player player) {
        List<Map<String,Object>> slots=new ArrayList<>();for(int i=0;i<player.getInventory().getSize();i++)slots.add(describe(player.getInventory().getItem(i),i));
        return Map.of("uuid",player.getUniqueId().toString(),"name",player.getName(),"observed_at",Instant.now().toString(),"slots",slots);
    }
    private Map<String,Object> replace(Player player,int slot,String expected,JsonObject input) {
        if(expected==null||!expected.equals("\""+hash(player.getInventory().getItem(slot))+"\""))throw new Failure(412,"slot changed; read inventory again");
        if(!input.has("material")||!input.has("amount"))throw new Failure(400,"material and amount required");
        Material material=Material.matchMaterial(input.get("material").getAsString());if(!input.get("amount").toString().matches("[0-9]{1,3}"))throw new Failure(400,"amount must be an integer");int amount=input.get("amount").getAsInt();
        if(material==null||(!material.isItem()&&!material.isAir())||amount<0||amount>material.getMaxStackSize()||material.isAir()&&amount!=0)throw new Failure(400,"invalid material or amount");
        ItemStack next=amount==0?null:new ItemStack(material,amount);
        if(next!=null){
            ItemMeta meta=next.getItemMeta();
            if(input.has("display_name")){String name=input.get("display_name").getAsString();if(name.length()>128)throw new Failure(400,"name too long");meta.displayName(Component.text(name));}
            if(input.has("lore")){var lines=input.getAsJsonArray("lore");if(lines.size()>16)throw new Failure(400,"too many lore lines");List<Component> lore=new ArrayList<>();for(var line:lines){String text=line.getAsString();if(text.length()>256)throw new Failure(400,"lore line too long");lore.add(Component.text(text));}meta.lore(lore);}
            next.setItemMeta(meta);
        }
        player.getInventory().setItem(slot,next);player.saveData();
        return describe(next,slot);
    }
    private Map<String,Object> resources() {
        // This is an explicitly bounded view of loaded entities and online inventories,
        // not an assertion about unloaded chunks, offline players or container contents.
        Map<String,Long> ground=new TreeMap<>(),players=new TreeMap<>();int seen=0;boolean truncated=false;
        outer: for(var world:Bukkit.getWorlds())for(var entity:world.getEntitiesByClass(Item.class)){
            if(++seen>10000){truncated=true;break outer;}ItemStack item=entity.getItemStack();ground.merge(item.getType().name(),(long)item.getAmount(),Long::sum);
        }
        int scanned=0;for(var player:Bukkit.getOnlinePlayers()){
            if(++scanned>1000){truncated=true;break;}
            for(ItemStack item:player.getInventory().getContents())if(item!=null&&!item.getType().isAir())players.merge(item.getType().name(),(long)item.getAmount(),Long::sum);
        }
        return Map.of("observed_at",Instant.now().toString(),"scope","loaded_ground_items_and_online_inventories","truncated",truncated,"ground",ground,"players",players);
    }
    private static final class Failure extends RuntimeException { final int status;Failure(int status,String message){super(message);this.status=status;} }
}
