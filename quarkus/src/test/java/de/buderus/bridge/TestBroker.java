package de.buderus.bridge;

import java.io.ByteArrayInputStream;
import java.io.DataInputStream;
import java.io.EOFException;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicBoolean;

final class TestBroker implements AutoCloseable {
    record Publish(String topic, String payload, int qos, boolean retained, int id, Socket socket) {}
    final List<Publish> messages = new CopyOnWriteArrayList<>();
    final List<Publish> wills = new CopyOnWriteArrayList<>();
    final AtomicBoolean acknowledge = new AtomicBoolean(true);
    final List<Socket> sockets = new CopyOnWriteArrayList<>();
    final ServerSocket server = new ServerSocket(0, 10, java.net.InetAddress.getLoopbackAddress());
    private final List<Thread> workers = new CopyOnWriteArrayList<>();
    private final Thread acceptor;

    TestBroker() throws IOException {
        acceptor = new Thread(() -> {
            while (!server.isClosed()) {
                try {
                    Socket socket = server.accept();
                    sockets.add(socket);
                    Thread worker = new Thread(() -> serve(socket));
                    workers.add(worker);
                    worker.start();
                } catch (IOException e) {
                    return;
                }
            }
        });
        acceptor.start();
    }

    String uri() {
        return "tcp://localhost:" + server.getLocalPort();
    }

    private void serve(Socket socket) {
        Publish will = null;
        boolean graceful = false;
        try (socket) {
            InputStream input = socket.getInputStream();
            while (true) {
                int header = input.read();
                if (header < 0) {
                    break;
                }
                int length = 0;
                int multiplier = 1;
                int digit;
                do {
                    digit = input.read();
                    if (digit < 0 || multiplier > 128 * 128 * 128) {
                        throw new EOFException();
                    }
                    length += (digit & 127) * multiplier;
                    multiplier *= 128;
                } while ((digit & 128) != 0);
                byte[] bytes = input.readNBytes(length);
                if (bytes.length != length) {
                    throw new EOFException();
                }
                DataInputStream packet = new DataInputStream(new ByteArrayInputStream(bytes));
                switch (header >> 4) {
                    case 1 -> {
                        packet.readUTF();
                        packet.readUnsignedByte();
                        int flags = packet.readUnsignedByte();
                        packet.readUnsignedShort();
                        packet.readUTF();
                        if ((flags & 4) != 0) {
                            String topic = packet.readUTF();
                            String payload = packet.readUTF();
                            will = new Publish(topic, payload, (flags >> 3) & 3, (flags & 32) != 0, 0, socket);
                        }
                        send(socket, new byte[] {0x20, 2, 0, 0});
                    }
                    case 3 -> {
                        String topic = packet.readUTF();
                        int qos = (header >> 1) & 3;
                        int id = qos > 0 ? packet.readUnsignedShort() : 0;
                        String payload = new String(packet.readAllBytes(), StandardCharsets.UTF_8);
                        Publish publication = new Publish(topic, payload, qos, (header & 1) != 0, id, socket);
                        messages.add(publication);
                        if (acknowledge.get() || topic.endsWith("/status")) {
                            ack(publication);
                        }
                    }
                    case 6 -> {
                        int id = packet.readUnsignedShort();
                        send(socket, new byte[] {0x70, 2, (byte) (id >> 8), (byte) id});
                    }
                    case 12 -> send(socket, new byte[] {(byte) 0xd0, 0});
                    case 14 -> {
                        graceful = true;
                        return;
                    }
                    default -> throw new IOException("unexpected packet");
                }
            }
        } catch (IOException ignored) {
            // Tests deliberately sever connections to exercise reconnect and Last Will.
        } finally {
            if (!graceful && will != null) {
                wills.add(will);
                messages.add(will);
            }
        }
    }

    void ack(Publish publish) throws IOException {
        if (publish.qos() > 0) {
            send(publish.socket(), new byte[] {(byte) (publish.qos() == 1 ? 0x40 : 0x50),
                    2, (byte) (publish.id() >> 8), (byte) publish.id()});
        }
    }

    private static void send(Socket socket, byte[] bytes) throws IOException {
        synchronized (socket) {
            OutputStream output = socket.getOutputStream();
            output.write(bytes);
            output.flush();
        }
    }

    @Override
    public void close() throws Exception {
        server.close();
        for (Socket socket : sockets) {
            socket.close();
        }
        acceptor.join(2000);
        for (Thread worker : workers) {
            worker.join(2000);
        }
    }
}
