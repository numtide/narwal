-- Basic structure of a binary cache:
--   ././
--  ├──  26xbg1ndr7hbcncrlf9nhx5is2b25d13.narinfo
--  ├──  4hcdxyjf9yiq7qf3i4548drb6sjmwa1v.narinfo
--  ├──  jwsdpq2yxw43ixalh93z726czz7bay2j.narinfo
--  ├──  log/
--  ├──  nar/
--  │   ├──  08242al70hn299yh1vk6il2cyahh6p86qvm72rmqz1z07q36vsk2.nar.xz
--  │   ├──  1767a9kz9xjpy5nh94d1prn3wv8rlcw7k9xhcsm0qcnx4l5qhq2n.nar.xz
--  │   ├──  17fm917985vcvrkrsckjb3i7q6rsxc4xlw8m1d6i5hdmxf9rxhh2.nar.xz
--  │   ├──  1ngi2dxw1f7khrrjamzkkdai393lwcm8s78gvs1ag8k3n82w7bvp.nar.xz
--  │   └──  1qva1j5l6gwjlj2xw69r3w8ldcgs14vp33hl7rm124r6q3fw13il.nar.xz
--  ├──  nix-cache-info
--  ├──  realisations/
--  │   └──  sha256:9d7d12c511042dac015ce38181f045b86da5a8d83a6d0364fa3b3fc48d28c203!out.doi
--  ├──  sl141d1g77wvhr050ah87lcyz2czdxa3.narinfo
--  └──  w19cxz37j5nrkg8w80y91bga89310jgi.narinfo
--
-- +goose Up
-- +goose StatementBegin

create type object_type as enum('nar', 'narinfo', 'ls', 'drv');
create type compression_type as enum ('br', 'xz', 'bzip2', 'gzip', 'zstd', 'none');

create table object
(
    hash varchar(52) not null,
    object_type object_type not null,
    compression_type compression_type not null,
    bucket varchar(128) not null,
    path varchar(128) not null,
    size bigint constraint positive_size check (size > 0) not null,
    created_at timestamp not null,
    last_accessed_at timestamp,

    primary key (hash, object_type, compression_type)
);

create index idx_object_type on object(object_type);
create unique index idx_object_path on object(path);

create table nar_info
(
    hash char(32) primary key,
    store_path varchar(1024) not null,
    compression compression_type not null,

    file_hash varchar(128) not null,
    file_size bigint constraint positive_file_size check (file_size > 0) not null,

    nar_hash varchar(128) not null,
    nar_size bigint constraint positive_nar_size check (nar_size > 0) not null,

    deriver varchar(1024) not null
);

create table nar_info_reference
(
    hash varchar(32) not null,
    refers_to varchar(32) not null,
    primary key (hash, refers_to)
);

create index idx_nar_info_reference_hash on nar_info_reference(hash);

create table nar_info_signature
(
    hash varchar(32) not null,
    name varchar(128) not null,
    data varchar(512) not null,
    primary key (hash, name)
);

create index idx_nar_info_signature_hash on nar_info_signature(hash);
create index idx_nar_info_signature_name on nar_info_signature(name);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop index idx_nar_info_signature_name;
drop index idx_nar_info_reference_hash;
drop index idx_nar_info_signature_hash;
drop index idx_object_type;
drop index idx_object_path;



drop table nar_info_signature;
drop table nar_info_reference;
drop table nar_info;
drop table object;

drop type compression_type;
drop type object_type;

-- +goose StatementEnd
